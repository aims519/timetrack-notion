package cli

import (
	"fmt"
	"os"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/spf13/cobra"
)

func newInitCmd(a *app) *cobra.Command {
	var tasksDbID string
	var projectsDbID string
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "initialise Notion connection details",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := timeparse.CurrentLocation()
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

			//TODO create output doc
			fmt.Printf("Tasks: %v, Projects: %v\n", tasksDataSourceID, projectsDataSourceID)

			return nil
		},
	}

	initCmd.Flags().StringVar(&tasksDbID, "tasks-db", "", "set tasks database ID")
	initCmd.Flags().StringVar(&projectsDbID, "projects-db", "", "set projects database ID")

	return initCmd
}
