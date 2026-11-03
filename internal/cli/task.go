package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/aims519/timetrack-notion/internal/taskcache"
	"github.com/aims519/timetrack-notion/internal/timeparse"
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

			loc, err := timeparse.CurrentLocation()
			if err != nil {
				return err
			}

			c := &refreshClock{}
			httpClient := http.Client{Timeout: 5 * time.Second} // 5 seconds per request
			token, exists := os.LookupEnv("NOTION_TOKEN")
			if !exists || token == "" {
				return &clierr.Error{Code: clierr.NoToken, Message: "Notion API token not set (NOTION_TOKEN)"}
			}

			notionClient := notion.NewClient(notion.BaseURL, token, &httpClient)

			doc, err := executeRefreshUpdates(cmd.Context(), a, c, notionClient, fullRefresh)
			if err != nil {
				return err
			}

			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, doc, loc)
		},
	}

	refreshCmd.Flags().BoolVar(&fullRefresh, "full", false, "also detect deleted or trashed tasks")

	return refreshCmd
}

type refreshClock struct{}

func (c *refreshClock) NowUTC() time.Time {
	return time.Now().UTC()
}

func executeRefreshUpdates(ctx context.Context, a *app, clock *refreshClock, nc *notion.Client, fullRefresh bool) (output.RefreshDoc, error) {
	start := time.Now()

	cont, err := a.db.NotionContext(ctx, store.DefaultContextID)
	if err != nil {
		return output.RefreshDoc{}, err
	}

	if cont.TasksDataSourceID == nil {
		return output.RefreshDoc{}, &clierr.Error{Code: clierr.NotInitialised, Message: "no tasks data source id found, run 'tt init' to set up connection to Notion"}
	}

	res, err := taskcache.Refresh(ctx, clock, a.db, nc, *cont.TasksDataSourceID, fullRefresh)
	if err != nil {
		return output.RefreshDoc{}, err
	}

	doc := output.NewRefreshDoc(fullRefresh,
		res.RefreshedAt,
		time.Since(start),
		res.FetchedCount,
		res.GoneCount, nil)

	return doc, nil
}
