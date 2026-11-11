// Package sync pushes local time entries to Notion
package sync

import (
	"context"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/gofrs/flock"
)

// Run executes all pending sync operations from the database. Will fail with sync_busy
// if another process holds the lock
func Run(ctx context.Context, lockPath string) error {
	fileLock := flock.New(lockPath)

	success, err := fileLock.TryLock()
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "unable to acquire sync lock", Cause: err, Details: map[string]any{"lock_path": lockPath}}
	}
	if !success {
		return &clierr.Error{Code: clierr.SyncBusy, Message: "another sync operation is in progress"}
	}

	defer func() { _ = fileLock.Unlock() }()
	// TODO sync work

	return nil
}
