package cli

import (
	"context"
	"fmt"
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
	taskCmd.AddCommand(newListCmd(a))

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
			token, exists := os.LookupEnv("NOTION_TOKEN")
			if !exists || token == "" {
				return &clierr.Error{Code: clierr.NoToken, Message: "Notion API token not set (NOTION_TOKEN)"}
			}

			notionClient := notion.NewClient(a.notionBaseURL, token, a.notionHTTPClient)

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

	if cont.ProjectsDataSourceID == nil {
		return output.RefreshDoc{}, &clierr.Error{Code: clierr.NotInitialised, Message: "no projects data source id found, run 'tt init' to set up connection to Notion"}
	}

	res, err := taskcache.Refresh(ctx, clock, a.db, nc, *cont.TasksDataSourceID, *cont.ProjectsDataSourceID, fullRefresh, cont.PropertyMap)
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

func newListCmd(a *app) *cobra.Command {
	var limit int
	var all = false
	var projectName string
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "list details of cached Notion tasks",
		Long:  "",
		RunE: func(cmd *cobra.Command, args []string) error {
			// No args expected
			if len(args) > 0 {
				return &clierr.Error{Code: clierr.Usage, Message: fmt.Sprintf("unknown argument received: %q", args[0])}
			}

			if cmd.Flag("limit").Changed && limit < 1 {
				return &clierr.Error{Code: clierr.Usage, Message: "--limit must be at least 1"}
			}

			if cmd.Flag("project").Changed && projectName == "" {
				return &clierr.Error{Code: clierr.Usage, Message: "--project must not be empty"}
			}

			var projectID *string
			if cmd.Flag("project").Changed {
				projects, err := a.db.ProjectsByName(cmd.Context(), projectName)
				if err != nil {
					return err
				}

				if len(projects) == 0 {
					return &clierr.Error{Code: clierr.NotFound, Message: fmt.Sprintf("no project found that matches %q", projectName), Details: map[string]any{"selector": projectName}}
				}

				if len(projects) > 1 {
					refs := make([]output.ProjectRef, len(projects))
					for i, v := range projects {
						refs[i] = output.ProjectRef{PageID: v.PageID, Name: v.Name}
					}

					return &clierr.Error{Code: clierr.AmbiguousSelector, Message: fmt.Sprintf("more than one project matches %q", projectName), Details: map[string]any{"selector": projectName, "candidates": refs}}
				}

				projectID = &projects[0].PageID
			}

			loc, err := timeparse.CurrentLocation()
			if err != nil {
				return err
			}

			// Auto refresh tasks if Notion token set
			var warnings []output.Warning
			if token := os.Getenv("NOTION_TOKEN"); token != "" {
				warnings, err = autoRefreshTaskCache(cmd.Context(), a, token)
				if err != nil {
					return err
				}
			}

			// Fetch tasks
			tasks, truncated, err := a.db.ListTasks(cmd.Context(), limit, all, projectID)
			if err != nil {
				return err
			}

			// Fetch short ID mappings for all tasks
			allIDs, err := a.db.AllTaskIDs(cmd.Context())
			if err != nil {
				return err
			}
			shortIDMap, err := taskcache.ShortIDs(allIDs)
			if err != nil {
				return err
			}

			doc := mapDbTasks(tasks, shortIDMap, truncated, warnings)
			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, doc, loc)
		},
	}

	listCmd.Flags().StringVar(&projectName, "project", "", "provide a project name to filter results")
	listCmd.Flags().BoolVar(&all, "all", all, "include 'done' tasks in results")
	listCmd.Flags().IntVar(&limit, "limit", store.NoLimit, "optionally limit the number of results")

	return listCmd
}

func autoRefreshTaskCache(ctx context.Context, a *app, notionToken string) ([]output.Warning, error) {
	cont, err := a.db.NotionContext(ctx, store.DefaultContextID)
	if err != nil {
		return nil, err
	}

	// If Notion context not initialised, skip silently
	if cont.TasksDataSourceID == nil {
		return nil, nil
	}
	if cont.ProjectsDataSourceID == nil {
		return nil, nil
	}

	// Also skip if no task mappings saved
	if cont.PropertyMap == nil {
		return nil, nil
	}

	c := &refreshClock{}
	notionClient := notion.NewClient(a.notionBaseURL, notionToken, a.notionHTTPClient)

	res, err := taskcache.AutoRefresh(ctx, c, a.db, notionClient, *cont.TasksDataSourceID, *cont.ProjectsDataSourceID, cont.PropertyMap)
	if err != nil {
		return []output.Warning{{Code: output.TaskCacheStale, Message: fmt.Sprintf("auto task refresh failed: %v", err)}}, nil
	}

	if res.BackoffSkip {
		return []output.Warning{{Code: output.TaskCacheStale, Message: "skipping refresh due to recent failure, using cached tasks"}}, nil
	}

	return nil, nil
}
