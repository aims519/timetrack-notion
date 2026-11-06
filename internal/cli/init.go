package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

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

			currentNotionContext, err := a.db.NotionContext(cmd.Context(), store.DefaultContextID)
			if err != nil {
				return err
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

			// If data sources missing, return error
			var resolvedTasksDataSourceID, resolvedProjectsDataSourceID string
			if tasksDataSourceID == nil && currentNotionContext.TasksDataSourceID == nil {
				return &clierr.Error{Code: clierr.NotInitialised, Message: "Notion tasks data source ID not set, retry with --tasks-db DATABASE_ID"}
			} else if tasksDataSourceID != nil {
				resolvedTasksDataSourceID = *tasksDataSourceID
			} else {
				resolvedTasksDataSourceID = *currentNotionContext.TasksDataSourceID
			}

			if projectsDataSourceID == nil && currentNotionContext.ProjectsDataSourceID == nil {
				return &clierr.Error{Code: clierr.NotInitialised, Message: "Notion projects data source ID not set, retry with --projects-db DATABASE_ID"}
			} else if projectsDataSourceID != nil {
				resolvedProjectsDataSourceID = *projectsDataSourceID
			} else {
				resolvedProjectsDataSourceID = *currentNotionContext.ProjectsDataSourceID
			}

			// Find property mappings
			taskProps, taskPropWarnings, err := mapTaskProperties(cmd.Context(), notionClient, resolvedTasksDataSourceID, resolvedProjectsDataSourceID)
			if err != nil {
				return err
			}

			// TODO Run refresh

			// TODO Look up project name from cache

			// TODO Create time entries database

			// Update Context in DB
			updatedContext := mergeNotionContextChanges(&currentNotionContext,
				&resolvedProjectsDataSourceID,
				&resolvedTasksDataSourceID,
				taskProps,
				nil)
			err = a.db.SaveNotionContext(cmd.Context(), currentNotionContext)
			if err != nil {
				return err
			}

			allWarnings := taskPropWarnings
			doc := output.NewInitDoc(output.NotionContext{
				ContextID:            updatedContext.ID,
				TasksDataSourceID:    updatedContext.TasksDataSourceID,
				ProjectsDataSourceID: updatedContext.ProjectsDataSourceID,
				EntriesDataSourceID:  updatedContext.EntriesDataSourceID,
			}, allWarnings)
			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, doc, loc)
		},
	}

	initCmd.Flags().StringVar(&tasksDbID, "tasks-db", "", "set tasks database ID")
	initCmd.Flags().StringVar(&projectsDbID, "projects-db", "", "set projects database ID")
	initCmd.Flags().StringVar(&projectName, "project", "", "name of the context's default project")

	return initCmd
}

func mergeNotionContextChanges(nc *store.NotionContext, projectsDataSourceID, tasksDataSourceID *string, tasksPropertyMappings, projectsPropertyMappings map[string]string) store.NotionContext {
	nc.TasksDataSourceID = tasksDataSourceID
	nc.ProjectsDataSourceID = projectsDataSourceID

	// Property map always replaced fully since tasks and project lookups are always made together
	propertyMap := map[string]string{}
	for key, val := range tasksPropertyMappings {
		propertyMap[key] = val
	}
	for key, val := range projectsPropertyMappings {
		propertyMap[key] = val
	}
	nc.PropertyMap = propertyMap

	return *nc
}

// mapTaskProperties gets the tasks data source from Notion and attempts to find required/optional properties.
// returns an error if required properties can't be found, and warnings for any missing optionals
func mapTaskProperties(ctx context.Context, nc *notion.Client, tasksDataSourceID, projectsDataSourceID string) (map[string]string, []output.Warning, error) {
	ds, err := nc.GetDataSource(ctx, tasksDataSourceID)
	if err != nil {
		return nil, nil, err
	}

	warnings := []output.Warning{}
	keys := []string{"task", "project", "order", "done"}
	results := map[string]string{}

	for _, k := range keys {
		if k == "task" {
			// Exactly one property with type == "title"
			titleProps := filterDataSourcePropertiesByType(ds.Properties, "title")
			if len(titleProps) != 1 {
				return nil, warnings, &clierr.Error{Code: clierr.NotFound, Message: "invalid tasks data source: no \"title\" property found", Details: map[string]any{"data_source_id": tasksDataSourceID}}
			}

			for key := range titleProps {
				results[k] = key
			}

			continue
		}

		if k == "project" {
			// Find relation that matches the projects data source ID
			relProps := filterDataSourcePropertiesByType(ds.Properties, "relation")
			if len(relProps) == 0 {
				return nil, warnings, &clierr.Error{Code: clierr.NotFound, Message: "invalid tasks data source: no project relational property found", Details: map[string]any{"data_source_id": tasksDataSourceID}}
			}

			for _, p := range relProps {
				if p.Relation.DataSourceID == projectsDataSourceID {
					results[k] = p.Name
					break
				}
			}

			if _, ok := results[k]; !ok {
				return nil, warnings, &clierr.Error{Code: clierr.NotFound, Message: "invalid tasks data source: no project relational property found", Details: map[string]any{"data_source_id": tasksDataSourceID}}
			}

			continue
		}

		if k == "order" {
			// Find number property that is appropriately named. Warn if not found
			numProps := filterDataSourcePropertiesByType(ds.Properties, "number")
			for _, p := range numProps {
				if strings.ToLower(p.Name) == k {
					results[k] = p.Name
					break
				}
			}

			if _, ok := results[k]; !ok {
				warnings = append(warnings, output.Warning{Code: output.OrderPropertyMissing, Message: "tasks data source doesn't have a number-type Order property", Details: map[string]any{"data_source_id": tasksDataSourceID}})
				continue
			}
		}

		if k == "done" {
			// Find checkbox property that is appropriately named. Error if not found
			cbProps := filterDataSourcePropertiesByType(ds.Properties, "checkbox")

			for _, p := range cbProps {
				if strings.ToLower(p.Name) == k {
					results[k] = p.Name
					break
				}
			}

			if _, ok := results[k]; !ok {
				return nil, warnings, &clierr.Error{Code: clierr.NotFound, Message: "invalid tasks data source: no Done property found, add a checkbox property 'Done' to your data source and retry", Details: map[string]any{"data_source_id": tasksDataSourceID}}
			}

			continue
		}
	}

	return results, warnings, nil
}

func filterDataSourcePropertiesByType(props map[string]notion.DataSourceProperty, selector string) map[string]notion.DataSourceProperty {
	results := map[string]notion.DataSourceProperty{}
	for key, p := range props {
		if p.Type == selector {
			results[key] = p
		}
	}

	return results
}
