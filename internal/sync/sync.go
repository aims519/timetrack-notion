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

	pending, err := s.PendingEntries(ctx)
	if err != nil {
		return err
	}

	// Create/Update entry pages
	for _, p := range pending {
		result := syncPendingEntry(ctx, *notionContext.EntriesDataSourceID, p, s, m, clock, attemptedAtUTC)

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

// syncPendingEntry updates Notion with the details of a pending entry, and records the sync result
// in the database
func syncPendingEntry(ctx context.Context, entriesDataSourceID string, e store.Entry, s *store.Store, m notionEntryManager, clock clock, attemptedAtUTC time.Time) syncResult {
	// set create_attempted_at
	err := s.SyncAttempt(ctx, e.ID, attemptedAtUTC)
	if err != nil {
		return handleSyncError(err)
	}

	var description string
	if e.Description != nil {
		description = *e.Description
	}

	entryPage, found, err := m.FindEntryPage(ctx, entriesDataSourceID, "Local ID", e.ID)
	if err != nil {
		return handleSyncError(err)
	}

	var entryPageID *string
	if found {
		updatedPage, err := m.UpdateEntryPage(ctx, entryPage.ID, entriesDataSourceID, *e.TaskName, description, e.ID, *e.TaskPageID, e.StartUTC, *e.EndUTC)
		if err != nil {
			return handleSyncError(err)
		}
		entryPageID = &updatedPage.ID
	} else {
		createdPage, err := m.CreateEntryPage(ctx, entriesDataSourceID, *e.TaskName, description, e.ID, *e.TaskPageID, e.StartUTC, *e.EndUTC)
		if err != nil {
			return handleSyncError(err)
		}
		entryPageID = &createdPage.ID
	}

	// success case
	syncedAt := clock.NowUTC()
	return syncResultSuccess(*entryPageID, syncedAt)
}

// handleSyncError maps an error encountered during a sync operation to Notion to a syncResult,
// which should then be used to update the entry in the database accordingly
func handleSyncError(err error) syncResult {
	// TODO handle too many request errors with retries, for now fail the sync
	var tooManyReq *notion.TooManyRequestsError
	if errors.As(err, &tooManyReq) {
		return syncResultFailed(tooManyReq.Error())
	}

	var cliErr *clierr.Error
	if errors.As(err, &cliErr) {
		switch cliErr.Code {
		case clierr.AccessDenied:
			return syncResultBlocked(cliErr.Message)
		case clierr.Usage:
			return syncResultBlocked(cliErr.Message)
		case clierr.NotFound:
			return syncResultBlocked(cliErr.Message)
		default:
			return syncResultFailed(cliErr.Error())
		}
	}

	return syncResultFailed(err.Error())
}
