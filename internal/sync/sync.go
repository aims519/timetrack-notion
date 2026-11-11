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

	// Create/Update entry pages
	for _, p := range pending {
		result, err := syncPendingEntry(ctx, *notionContext.EntriesDataSourceID, p, s, m, clock, attemptedAtUTC)
		if err != nil {
			return err
		}

		// update db entry with synced_at, sync_error, sync_state
		err = s.CompleteSync(ctx, p.ID, result.Status, result.SyncError, result.EntryPageID, attemptedAtUTC, result.SyncedAt)
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

	// Update existing page
	if entryPageID != nil {
		_, err := m.UpdateEntryPage(ctx, *entryPageID, entriesDataSourceID, title, description, e.ID, *e.TaskPageID, e.StartUTC, *e.EndUTC)
		if err != nil {
			return handleSyncError(err)
		}
	} else { // create new page
		createdPage, err := m.CreateEntryPage(ctx, entriesDataSourceID, title, description, e.ID, *e.TaskPageID, e.StartUTC, *e.EndUTC)
		if err != nil {
			return handleSyncError(err)
		}
		entryPageID = &createdPage.ID
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
		return syncResult{}, &clierr.Error{Code: clierr.Offline, Message: "notion unresponsive, too many requests"}
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
