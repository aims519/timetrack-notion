package cli

import (
	"os"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
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

			token := os.Getenv("NOTION_TOKEN")

			if token == "" {
				return &clierr.Error{Code: clierr.NoToken, Message: "notion API token not found, make sure NOTION_TOKEN is set"}
			}
			notionClient := notion.NewClient(a.notionBaseURL, token, a.notionHTTPClient)

			return sync.Run(cmd.Context(), a.db, notionClient, &syncClock{}, lockFilePath)
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

// syncClock gives sync the real time, in UTC
type syncClock struct{}

// NowUTC returns the current time in UTC
func (c *syncClock) NowUTC() time.Time {
	return time.Now().UTC()
}
