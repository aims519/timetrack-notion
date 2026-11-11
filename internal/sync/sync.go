package sync

import (
	"context"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/gofrs/flock"
)

// Run starts a sync of local entries with Notion. Only one sync can run
// at a time
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
