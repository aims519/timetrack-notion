// Package sync pushes local time entries to Notion
package sync

import (
	"context"
	"errors"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/gofrs/flock"
)

// Run executes all pending sync operations from the database. Will fail with sync_busy
// if another process holds the lock
func Run(ctx context.Context, s *store.Store, m notionEntryManager, clock clock, lockPath string) error {
	fileLock := flock.New(lockPath)

	success, err := fileLock.TryLock()
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "unable to acquire sync lock", Cause: err, Details: map[string]any{"lock_path": lockPath}}
	}
	if !success {
		return &clierr.Error{Code: clierr.SyncBusy, Message: "another sync operation is in progress"}
	}

	defer func() { _ = fileLock.Unlock() }()
	attemptedAtUTC := clock.NowUTC()

	// Fetch pending entries from Notion
	notionContext, err := s.NotionContext(ctx, store.DefaultContextID)
	if err != nil {
		return err
	}
	if notionContext.EntriesDataSourceID == nil {
		return &clierr.Error{Code: clierr.NotInitialised, Message: "Notion context not fully initialised, run 'tt init'"}
	}

	pending, err := s.PendingEntries(ctx, store.DefaultContextID)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		return nil
	}

	// Check data source exists, abort if not
	err = checkDataSource(ctx, m, *notionContext.EntriesDataSourceID)
	if err != nil {
		return err
	}

	// Create/Update entry pages
	for _, p := range pending {
		result, err := syncPendingEntry(ctx, *notionContext.EntriesDataSourceID, p, s, m, clock, attemptedAtUTC)
		if err != nil {
			return err
		}

		// update db entry with synced_at, sync_error, sync_state
		_, err = s.CompleteSync(ctx, p.ID, result.Status, result.SyncError, result.EntryPageID, p.UpdatedAt, result.SyncedAt)
		if err != nil {
			return err
		}
	}

	return nil
}

// clock is an interface to allow testability of code that requires
// the current time
type clock interface {
	NowUTC() time.Time
}

// notionEntryManager is an interface that allows for testability of sync package functions
// without requiring a real Notion client
type notionEntryManager interface {
	GetDataSource(ctx context.Context, dataSourceID string) (notion.DataSource, error)
	EntryPage(ctx context.Context, pageID, dataSourceID string) (notion.Page, error)
	FindEntryPage(ctx context.Context, entriesDataSourceID, localIDKey, localIDValue string) (notion.Page, bool, error)
	CreateEntryPage(ctx context.Context, dataSourceID, title, description, localID, taskPageID string, start, end time.Time) (notion.Page, error)
	UpdateEntryPage(ctx context.Context, pageID, dataSourceID, title, description, localID, taskPageID string, start, end time.Time) (notion.Page, error)
	TrashEntryPage(ctx context.Context, pageID, dataSourceID string) (notion.Page, error)
}

// syncPendingEntry updates Notion with the details of a pending entry, and records the sync result
// in the database
func syncPendingEntry(ctx context.Context, entriesDataSourceID string, e store.Entry, s *store.Store, m notionEntryManager, clock clock, attemptedAtUTC time.Time) (syncResult, error) {
	// set create_attempted_at
	err := s.SyncAttempt(ctx, e.ID, attemptedAtUTC)
	if err != nil {
		return syncResult{}, err
	}

	// default title to description, else task name
	var title string
	var description string
	if e.Description != nil {
		title = *e.Description
		description = *e.Description
	} else {
		title = *e.TaskName
	}

	// check if page already exists
	entryPageID := e.EntryPageID
	if entryPageID == nil {
		entryPage, found, err := m.FindEntryPage(ctx, entriesDataSourceID, "Local ID", e.ID)
		if err != nil {
			return handleSyncError(err)
		}
		if found {
			entryPageID = &entryPage.ID
		}
	}

	if entryPageID != nil && e.DeletedAt == nil {
		err = updatePage(ctx, m, entriesDataSourceID, *entryPageID, title, description, e)
	} else if entryPageID != nil && e.DeletedAt != nil {
		err = trashPage(ctx, m, entriesDataSourceID, *entryPageID)
	} else {
		entryPageID, err = createPage(ctx, m, entriesDataSourceID, entryPageID, title, description, e)
	}
	if err != nil {
		return handleSyncError(err)
	}

	// success case
	syncedAt := clock.NowUTC()
	return syncResultSuccess(*entryPageID, syncedAt), nil
}

// handleSyncError maps an error encountered during a sync operation to Notion to a syncResult,
// which should then be used to update the entry in the database accordingly
func handleSyncError(err error) (syncResult, error) {
	// TODO handle too many request errors with retries, for now fail the sync
	var tooManyReq *notion.TooManyRequestsError
	if errors.As(err, &tooManyReq) {
		return syncResult{}, &clierr.Error{Code: clierr.Offline, Message: "notion unresponsive, too many requests", Cause: err}
	}

	if errors.Is(err, notion.ErrDataSourceUnusable) {
		return syncResult{}, err
	}

	var cliErr *clierr.Error
	if errors.As(err, &cliErr) {
		switch cliErr.Code {
		case clierr.AccessDenied:
			return syncResult{}, &clierr.Error{Code: clierr.AccessDenied, Message: "notion access denied, token invalid"}
		case clierr.Usage:
			return syncResultBlocked(cliErr.Message), nil
		case clierr.NotFound:
			return syncResultBlocked(cliErr.Message), nil
		case clierr.Offline:
			return syncResult{}, &clierr.Error{Code: clierr.Offline, Message: "notion unresponsive"}
		default:
			return syncResultFailed(cliErr.Error()), nil
		}
	}

	return syncResultFailed(err.Error()), nil
}

// syncResult represents the result of syncing a single local time entry
type syncResult struct {
	EntryPageID *string
	Status      string
	SyncedAt    *time.Time
	SyncError   *string
}

// syncResultSuccess constructs a syncResult with status of 'synced', with appropriate
// properties set
func syncResultSuccess(entryPageID string, syncedAt time.Time) syncResult {
	return syncResult{
		EntryPageID: &entryPageID,
		Status:      "synced",
		SyncedAt:    &syncedAt,
		SyncError:   nil,
	}
}

// syncResultFailed constructs a syncResult with a status of 'failed', with appropriate
// properties set
func syncResultFailed(syncError string) syncResult {
	return syncResult{
		EntryPageID: nil,
		Status:      "failed",
		SyncedAt:    nil,
		SyncError:   &syncError,
	}
}

// syncResultFailed constructs a syncResult with a status of 'blocked', with appropriate
// properties set
func syncResultBlocked(syncError string) syncResult {
	return syncResult{
		EntryPageID: nil,
		Status:      "blocked",
		SyncedAt:    nil,
		SyncError:   &syncError,
	}
}

// checkDataSource returns an error if the entries data source is missing or trashed in Notion,
// wrapping ErrDataSourceUnusable, or if Notion couldn't be asked. At the start of a run any
// error stops the run; after a page 404 the error goes through handleSyncError
func checkDataSource(ctx context.Context, m notionEntryManager, dataSourceID string) error {
	ds, err := m.GetDataSource(ctx, dataSourceID)
	var cliErr *clierr.Error
	if errors.As(err, &cliErr) && cliErr.Code == clierr.NotFound {
		return &clierr.Error{Code: clierr.NotFound, Message: "saved entries data source not found in Notion, restore or re-share", Cause: notion.ErrDataSourceUnusable}
	}
	if err != nil {
		return err
	}
	if ds.InTrash {
		return &clierr.Error{Code: clierr.NotFound, Message: "saved entries data source trashed in Notion, restore or init", Cause: notion.ErrDataSourceUnusable}
	}

	return nil
}

// createPage creates a Notion page for an entry and returns its ID. A 404 could mean the data
// source is gone, so it's checked again: if the check fails, that error is returned instead
// of the 404
func createPage(ctx context.Context, m notionEntryManager, entriesDataSourceID string, entryPageID *string, title, description string, e store.Entry) (*string, error) {
	createdPage, err := m.CreateEntryPage(ctx, entriesDataSourceID, title, description, e.ID, *e.TaskPageID, e.StartUTC, *e.EndUTC)

	var cliErr *clierr.Error
	if errors.As(err, &cliErr) && cliErr.Code == clierr.NotFound {
		if err := checkDataSource(ctx, m, entriesDataSourceID); err != nil {
			return nil, err
		}
	}

	if err != nil {
		return nil, err
	}

	return &createdPage.ID, nil
}

// updatePage updates an entry's existing Notion page. A 404 could mean the data source is gone
// or the entry's own page was deleted, so the data source is checked again: if the check
// fails, that error is returned instead of the 404
func updatePage(ctx context.Context, m notionEntryManager, entriesDataSourceID string, entryPageID string, title, description string, e store.Entry) error {
	_, err := m.UpdateEntryPage(ctx, entryPageID, entriesDataSourceID, title, description, e.ID, *e.TaskPageID, e.StartUTC, *e.EndUTC)

	var cliErr *clierr.Error
	if errors.As(err, &cliErr) && cliErr.Code == clierr.NotFound {
		if err := checkDataSource(ctx, m, entriesDataSourceID); err != nil {
			return err
		}
	}

	return err
}

// trashPage updates the in_trash property of the corresponding Notion page to true.
// A 404 could mean the data source is gone, so the data source is checked again: if the check
// fails, that error is returned instead of the 404. If a 400 is returned, this could be because the page is
// already trashed, and if so no error is returned as the sync can be marked successful
func trashPage(ctx context.Context, m notionEntryManager, entriesDataSourceID string, entryPageID string) error {
	_, err := m.TrashEntryPage(ctx, entryPageID, entriesDataSourceID)

	var cliErr *clierr.Error
	if errors.As(err, &cliErr) {
		if cliErr.Code == clierr.NotFound {
			if err := checkDataSource(ctx, m, entriesDataSourceID); err != nil {
				return err
			}
		}
		if cliErr.Code == clierr.Usage {
			if page, lookupErr := m.EntryPage(ctx, entryPageID, entriesDataSourceID); lookupErr == nil && page.InTrash {
				return nil
			}
			return err
		}
	}

	return err
}
