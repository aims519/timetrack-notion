package cli

import (
	"github.com/aims519/timetrack-notion/internal/sync"
	"github.com/spf13/cobra"
)

// newSyncCmd builds and returns a new instance of the sync command
func newSyncCmd(a *app) *cobra.Command {
	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "sync local tracked entries with Notion",
		RunE: func(cmd *cobra.Command, args []string) error {
			lockFilePath := syncLockFilePath(a.dbFilePath)

			return sync.Run(cmd.Context(), lockFilePath)
		},
	}

	return syncCmd
}

// syncLockFilePath returns the sync lock file path for a given database file.
// Lock files always live next to their associated database file.
//
//	example:
//	dbPath := /some/path/tt.db
//	lockPath := /some/path/tt.db.lock
func syncLockFilePath(dbPath string) string {
	return dbPath + ".lock"
}
