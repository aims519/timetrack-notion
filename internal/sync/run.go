package sync

import (
	"context"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/gofrs/flock"
)

// clock is an interface to allow testability of code that requires
// the current time
type clock interface {
	NowUTC() time.Time
	Sleep(ctx context.Context, d time.Duration) error
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

// Run executes all pending sync operations from the database. Will fail with sync_busy
// if another process holds the lock
func Run(ctx context.Context, s *store.Store, m notionEntryManager, clock clock, lockPath string, retrySettings RetrySettings) error {
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

	// track consecutive network failures, abort the run if threshold is met
	networkErrorCount := 0

	// Check data source exists, abort if not (after retries)
	// Abort on first network failure in this case
	firstRequestSettings := retrySettings
	firstRequestSettings.maxNetworkFailures = 1
	_, err = withRetry(ctx, clock, firstRequestSettings, &networkErrorCount, func() (any, error) {
		return nil, checkDataSource(ctx, m, *notionContext.EntriesDataSourceID)
	})
	if err != nil && networkErrorCount >= retrySettings.maxNetworkFailures {
		return err
	}
	if err != nil {
		return err
	}

	// Create/Update entry pages
	for _, p := range pending {
		// set create_attempted_at, outside of retry logic
		err := s.SyncAttempt(ctx, p.ID, attemptedAtUTC)
		if err != nil {
			return err
		}

		result, err := withRetry(ctx, clock, retrySettings, &networkErrorCount, func() (syncResult, error) {
			return syncPendingEntry(ctx, *notionContext.EntriesDataSourceID, p, m, clock)
		})
		if err != nil && networkErrorCount >= retrySettings.maxNetworkFailures {
			return err
		}
		if err != nil {
			result, err = handleSyncError(err)
		}

		if err != nil {
			return err
		}

		// reset network failure counter following success
		networkErrorCount = 0

		// update db entry with synced_at, sync_error, sync_state
		_, err = s.CompleteSync(ctx, p.ID, result.Status, result.SyncError, result.EntryPageID, p.UpdatedAt, result.SyncedAt)
		if err != nil {
			return err
		}
	}

	return nil
}
