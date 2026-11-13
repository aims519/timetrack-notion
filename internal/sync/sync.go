// Package sync pushes local time entries to Notion
package sync

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/store"
)

// syncPendingEntry updates Notion with the details of a pending entry
func syncPendingEntry(ctx context.Context, entriesDataSourceID string, e store.Entry, m notionEntryManager, clock clock) (syncResult, error) {
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
			return syncResult{}, err
		}
		if found {
			entryPageID = &entryPage.ID
		}
	}

	// entries marked as deleted with no Notion page succeed
	if entryPageID == nil && e.DeletedAt != nil {
		syncedAt := clock.NowUTC()
		return syncResultSuccess(entryPageID, syncedAt), nil
	}

	var err error
	if entryPageID != nil && e.DeletedAt == nil {
		err = updatePage(ctx, m, entriesDataSourceID, *entryPageID, title, description, e)
	} else if entryPageID != nil && e.DeletedAt != nil {
		err = trashPage(ctx, m, entriesDataSourceID, *entryPageID)
	} else {
		entryPageID, err = createPage(ctx, m, entriesDataSourceID, title, description, e)
	}
	if err != nil {
		return syncResult{}, err
	}

	// success case
	syncedAt := clock.NowUTC()
	return syncResultSuccess(entryPageID, syncedAt), nil
}

// handleSyncError maps an error encountered during a sync operation to Notion to a syncResult,
// which should then be used to update the entry in the database accordingly
func handleSyncError(err error) (syncResult, error) {
	var tooManyReq *notion.TooManyRequestsError
	if errors.As(err, &tooManyReq) {
		// if this error gets here it must have failed its max retry attempts
		// recorded as a failure to try again next run
		return syncResultFailed(tooManyReq.Error()), nil
	}

	if errors.Is(err, context.Canceled) {
		return syncResult{}, &clierr.Error{Code: clierr.Cancelled, Message: "sync run was cancelled", Cause: err}
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
func syncResultSuccess(entryPageID *string, syncedAt time.Time) syncResult {
	return syncResult{
		EntryPageID: entryPageID,
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
// wrapping ErrDataSourceUnusable, or if Notion couldn't be asked
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
func createPage(ctx context.Context, m notionEntryManager, entriesDataSourceID string, title, description string, e store.Entry) (*string, error) {
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
			page, lookupErr := m.EntryPage(ctx, entryPageID, entriesDataSourceID)
			// return lookup error if one should occur, since we can't tell if the page is trashed or not
			if lookupErr != nil {
				return lookupErr
			}
			if page.InTrash {
				return nil
			}

			// return original usage error
			return err
		}
	}

	return err
}

// RetrySettings encapsulates all settings required to handle automatic retries
// of sync-related requests to Notion
type RetrySettings struct {
	maxAttempts        int
	maxNetworkFailures int
	base               time.Duration
	cap                time.Duration
	rnd                *rand.Rand
}

// DefaultRetrySettings builds RetrySettings with default values,
// using the provided rand.Source for jitter
func DefaultRetrySettings(source rand.Source) RetrySettings {
	return RetrySettings{
		maxAttempts:        5,
		maxNetworkFailures: 3,
		base:               1 * time.Second,
		cap:                30 * time.Second,
		rnd:                rand.New(source),
	}
}

// withRetry wraps calls to Notion that should be retried in the event of a 429 or 5xx response.
// If the network failure count reaches the max allowed, latest error is returned without a retry
func withRetry[T any](ctx context.Context, clock clock, settings RetrySettings, networkFailures *int, op func() (T, error)) (T, error) {
	var res T
	var err error
	for i := 1; i <= settings.maxAttempts; i++ {
		res, err = op()

		if errors.Is(err, context.Canceled) {
			return res, &clierr.Error{Code: clierr.Cancelled, Message: "operation was cancelled", Cause: err}
		}

		// bump network failure count to ensure correct count is recorded before any early returns
		if errors.Is(err, notion.ErrNetworkFailure) && networkFailures != nil {
			*networkFailures++
		}

		if i == settings.maxAttempts {
			return res, err
		}

		var wait time.Duration
		var tooManyReq *notion.TooManyRequestsError
		switch {
		case errors.As(err, &tooManyReq):
			if tooManyReq.RetryAfterSeconds != nil {
				wait = time.Duration(*tooManyReq.RetryAfterSeconds) * time.Second
			} else {
				// No retry specified, fall back to our own wait time + jitter
				wait = backOffWait(settings.base, settings.cap, i, settings.rnd)
			}
		case errors.Is(err, notion.ErrServerError):
			wait = backOffWait(settings.base, settings.cap, i, settings.rnd)
		case errors.Is(err, notion.ErrNetworkFailure):
			if networkFailures != nil && *networkFailures >= settings.maxNetworkFailures {
				return res, err
			}

			wait = backOffWait(settings.base, settings.cap, i, settings.rnd)
		default:
			// no retry needed
			return res, err
		}

		sleepErr := clock.Sleep(ctx, wait)
		if errors.Is(sleepErr, context.Canceled) {
			return res, &clierr.Error{Code: clierr.Cancelled, Message: "retry wait operation was cancelled", Cause: sleepErr}
		}
		if sleepErr != nil {
			return res, sleepErr
		}
	}

	return res, err
}

// backOffWait is a helper that returns a wait time for sync retries
func backOffWait(mainWait, maxWait time.Duration, attempt int, rnd *rand.Rand) time.Duration {
	bound := min(maxWait, mainWait<<(attempt-1))
	wait := time.Duration(rnd.Int64N(int64(bound)))

	return wait
}
