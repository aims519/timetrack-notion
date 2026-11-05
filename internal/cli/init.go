package cli

import (
	"fmt"
	"os"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/spf13/cobra"
)

func newInitCmd(a *app) *cobra.Command {
	var tasksDbID string
	var projectsDbID string
	var projectName string
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "initialise Notion connection details",
		RunE: func(cmd *cobra.Command, args []string) error {
			loc, err := timeparse.CurrentLocation()
			if err != nil {
				return err
			}

			token, exists := os.LookupEnv("NOTION_TOKEN")
			if !exists || token == "" {
				return &clierr.Error{Code: clierr.NoToken, Message: "Notion API token not set (NOTION_TOKEN)"}
			}

			if cmd.Flag("tasks-db").Changed && tasksDbID == "" {
				return &clierr.Error{Code: clierr.Usage, Message: "--tasks-db value must not be empty"}
			}

			if cmd.Flag("projects-db").Changed && projectsDbID == "" {
				return &clierr.Error{Code: clierr.Usage, Message: "--projects-db value must not be empty"}
			}

			if cmd.Flag("project").Changed && projectName == "" {
				return &clierr.Error{Code: clierr.Usage, Message: "--project name must not be empty"}
			}

			// Data source lookups
			notionClient := notion.NewClient(a.notionBaseURL, token, a.notionHTTPClient)
			var tasksDataSourceID *string
			if cmd.Flag("tasks-db").Changed {
				ds, err := notionClient.LookupDataSourceID(cmd.Context(), tasksDbID)
				if err != nil {
					return fmt.Errorf("unable to look up Notion Tasks data source ID: %w", err)
				}
				tasksDataSourceID = ds
			}

			var projectsDataSourceID *string
			if cmd.Flag("projects-db").Changed {
				ds, err := notionClient.LookupDataSourceID(cmd.Context(), projectsDbID)
				if err != nil {
					return fmt.Errorf("unable to look up Notion Projects data source ID: %w", err)
				}
				projectsDataSourceID = ds
			}

			// TODO Map properties

			// TODO Run refresh

			// TODO Look up project name from cache

			// TODO Create time entries database

			// Update Context in DB
			nc, err := a.db.NotionContext(cmd.Context(), store.DefaultContextID)
			if err != nil {
				return err
			}

			mergeNotionContextChanges(&nc, projectsDataSourceID, tasksDataSourceID)
			err = a.db.SaveNotionContext(cmd.Context(), nc)
			if err != nil {
				return err
			}

			doc := output.NewInitDoc(output.NotionContext{
				ContextID:            nc.ID,
				TasksDataSourceID:    nc.TasksDataSourceID,
				ProjectsDataSourceID: nc.ProjectsDataSourceID,
				EntriesDataSourceID:  nc.EntriesDataSourceID,
			}, nil)
			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, doc, loc)
		},
	}

	initCmd.Flags().StringVar(&tasksDbID, "tasks-db", "", "set tasks database ID")
	initCmd.Flags().StringVar(&projectsDbID, "projects-db", "", "set projects database ID")
	initCmd.Flags().StringVar(&projectName, "project", "", "name of the context's default project")

	return initCmd
}

func mergeNotionContextChanges(nc *store.NotionContext, projectsDataSourceID, tasksDataSourceID *string) {
	if tasksDataSourceID != nil {
		nc.TasksDataSourceID = tasksDataSourceID
	}
	if projectsDataSourceID != nil {
		nc.ProjectsDataSourceID = projectsDataSourceID
	}
}
