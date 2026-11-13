package cli

import (
	"context"
	"math/rand/v2"
	"os"
	"os/signal"
	"syscall"
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
			// Allow CTRL+C to send a cancellation during a sync run
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			lockFilePath := syncLockFilePath(a.dbFilePath)

			token := os.Getenv("NOTION_TOKEN")

			if token == "" {
				return &clierr.Error{Code: clierr.NoToken, Message: "notion API token not found, make sure NOTION_TOKEN is set"}
			}
			notionClient := notion.NewClient(a.notionBaseURL, token, a.notionHTTPClient)

			retrySettings := sync.DefaultRetrySettings(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			return sync.Run(ctx, a.db, notionClient, &syncClock{}, lockFilePath, retrySettings)
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

// Sleep starts a timer and waits for it to complete, interrupting if the given
// context is cancelled
func (c *syncClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
