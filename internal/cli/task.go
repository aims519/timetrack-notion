package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/aims519/timetrack-notion/internal/taskcache"
	"github.com/spf13/cobra"
)

func newTaskCmd(a *app) *cobra.Command {
	taskCmd := &cobra.Command{
		Use:     "task",
		Short:   "manage locally cached Notion tasks",
		Long:    "",
		Example: "",
	}

	taskCmd.AddCommand(newRefreshCmd(a))

	return taskCmd
}

func newRefreshCmd(a *app) *cobra.Command {
	var fullRefresh bool
	refreshCmd := &cobra.Command{
		Use:   "refresh",
		Short: "initiate a refresh of locally cached Notion tasks, ignoring TTL and back-off",
		Long:  "",
		RunE: func(cmd *cobra.Command, args []string) error {
			// No args expected
			if len(args) > 0 {
				return &clierr.Error{Code: clierr.Usage, Message: fmt.Sprintf("unknown argument received: %q", args[0])}
			}

			c := &refreshClock{}
			httpClient := http.Client{Timeout: 5 * time.Second} // 5 seconds per request
			token, exists := os.LookupEnv("NOTION_TOKEN")
			if !exists || token == "" {
				return &clierr.Error{Code: clierr.NoToken, Message: "Notion API token not set (NOTION_TOKEN)"}
			}

			notionClient := notion.NewClient(notion.BaseURL, token, &httpClient)

			return executeRefreshUpdates(cmd.Context(), a, c, notionClient, fullRefresh)
		},
	}

	refreshCmd.Flags().BoolVar(&fullRefresh, "full", false, "also detect deleted or trashed tasks")

	return refreshCmd
}

type refreshClock struct{}

func (c *refreshClock) NowUTC() time.Time {
	return time.Now().UTC()
}

func executeRefreshUpdates(ctx context.Context, a *app, clock *refreshClock, nc *notion.Client, fullRefresh bool) error {
	cont, err := a.db.NotionContext(ctx, store.DefaultContextID)
	if err != nil {
		return err
	}

	if cont.TasksDataSourceID == nil {
		return &clierr.Error{Code: clierr.NotInitialised, Message: "no tasks data source id found, run 'tt init' to set up connection to Notion"}
	}

	return taskcache.Refresh(ctx, clock, a.db, nc, *cont.TasksDataSourceID, fullRefresh)
}
