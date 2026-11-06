package cli

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/aims519/timetrack-notion/internal/taskcache"
	"github.com/aims519/timetrack-notion/internal/timeparse"
	"github.com/spf13/cobra"
)

func newInitCmd(a *app) *cobra.Command {
	var tasksDbID string
	var projectsDbID string
	var projectName string
	var parentPageID string
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

			if cmd.Flag("parent-page").Changed && parentPageID == "" {
				return &clierr.Error{Code: clierr.Usage, Message: "--parent-page ID must not be empty"}
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

			var resolvedEntriesDataSourceID string
			// Fail if no entries data source ID stored and no parent page ID provided
			if currentNotionContext.EntriesDataSourceID == nil && parentPageID == "" {
				return &clierr.Error{Code: clierr.NotInitialised, Message: "no entries database exists, try again with --parent-page PAGE_ID to allow it to be created"}
			}

			// Entries data source ID locally stored, check database still exists
			if currentNotionContext.EntriesDataSourceID != nil {
				entriesDS, err := notionClient.GetDataSource(cmd.Context(), *currentNotionContext.EntriesDataSourceID)
				if err != nil {
					return fmt.Errorf("saved entries data source no longer found in Notion, restore or re-share: %w", err)
				}

				if entriesDS.InTrash {
					return &clierr.Error{Code: clierr.NotInitialised, Message: "saved entries data source has been moved to Notion's trash, restore before retrying", Details: map[string]any{"entries_data_source_id": entriesDS.ID}}
				}

				if taskProp, found := entriesDS.Properties["Task"]; !found || taskProp.Relation.DataSourceID != resolvedTasksDataSourceID || taskProp.Type != "relation" {
					if !found || taskProp.Type != "relation" {
						return &clierr.Error{Code: clierr.NotInitialised, Message: "saved entries data source has no relationship to tasks database", Details: map[string]any{"entries_data_source_id": entriesDS.ID}}
					}

					if taskProp.Relation.DataSourceID != resolvedTasksDataSourceID {
						return &clierr.Error{Code: clierr.NotInitialised, Message: "saved entries data source relationship points to a different tasks database", Details: map[string]any{"entries_data_source_id": entriesDS.ID, "tasks_data_source_id": resolvedTasksDataSourceID, "task_relation_data_source_id": taskProp.Relation.DataSourceID}}
					}
				}

				resolvedEntriesDataSourceID = entriesDS.ID
			}

			// Refresh cache (full)
			clock := refreshClock{}
			_, err = taskcache.Refresh(cmd.Context(), &clock, a.db, notionClient, resolvedTasksDataSourceID, resolvedProjectsDataSourceID, true, taskProps)
			if err != nil {
				return err
			}

			// Attempt to resolve project name from updated cache
			var defaultProjectID *string
			if cmd.Flag("project").Changed {
				matches, err := a.db.ProjectsByName(cmd.Context(), projectName)
				if err != nil {
					return err
				}

				if len(matches) == 0 {
					return &clierr.Error{Code: clierr.NotFound, Message: fmt.Sprintf("no project found that matches %q", projectName), Details: map[string]any{"selector": projectName}}
				}

				if len(matches) > 1 {
					refs := make([]output.ProjectRef, len(matches))
					for i, v := range matches {
						refs[i] = output.ProjectRef{PageID: v.PageID, Name: v.Name}
					}

					return &clierr.Error{Code: clierr.AmbiguousSelector, Message: fmt.Sprintf("more than one project matches %q", projectName), Details: map[string]any{"selector": projectName, "candidates": refs}}
				}

				defaultProjectID = &matches[0].PageID
			}

			// Time entries database does not yet exist, create
			if currentNotionContext.EntriesDataSourceID == nil {
				resolvedEntriesDataSourceID, err = notionClient.CreateDatabase(cmd.Context(), parentPageID, resolvedTasksDataSourceID)
				if err != nil {
					return err
				}
			}

			// Update Context in DB
			updatedContext := mergeNotionContextChanges(&currentNotionContext,
				&resolvedProjectsDataSourceID,
				&resolvedTasksDataSourceID,
				&resolvedEntriesDataSourceID,
				defaultProjectID,
				taskProps)
			err = a.db.SaveNotionContext(cmd.Context(), currentNotionContext)
			if err != nil {
				return err
			}

			// Query waiting entries
			waitingCount, err := a.db.WaitingEntriesCount(cmd.Context())
			if err != nil {
				return err
			}

			allWarnings := taskPropWarnings
			doc := output.NewInitDoc(output.NotionContext{
				ContextID:            updatedContext.ID,
				TasksDataSourceID:    updatedContext.TasksDataSourceID,
				ProjectsDataSourceID: updatedContext.ProjectsDataSourceID,
				EntriesDataSourceID:  updatedContext.EntriesDataSourceID,
				DefaultProjectPageID: updatedContext.DefaultProjectPageID,
				PropertyMap:          updatedContext.PropertyMap,
			}, waitingCount, allWarnings)

			return output.Write(cmd.OutOrStdout(), cmd.ErrOrStderr(), *a.format, doc, loc)
		},
	}

	initCmd.Flags().StringVar(&tasksDbID, "tasks-db", "", "set tasks database ID")
	initCmd.Flags().StringVar(&projectsDbID, "projects-db", "", "set projects database ID")
	initCmd.Flags().StringVar(&projectName, "project", "", "name of the context's default project")
	initCmd.Flags().StringVar(&parentPageID, "parent-page", "", "ID of the parent page under which all other databases live")

	return initCmd
}

func mergeNotionContextChanges(nc *store.NotionContext, projectsDataSourceID, tasksDataSourceID, entriesDataSourceID, defaultProjectPageID *string, tasksPropertyMappings map[string]string) store.NotionContext {
	nc.EntriesDataSourceID = entriesDataSourceID
	nc.TasksDataSourceID = tasksDataSourceID

	// project data source changed if old and new are not nill, and values differ
	projectsDSUpdated := nc.ProjectsDataSourceID != nil && projectsDataSourceID != nil && *nc.ProjectsDataSourceID != *projectsDataSourceID
	nc.ProjectsDataSourceID = projectsDataSourceID

	// Property map always replaced fully
	nc.PropertyMap = tasksPropertyMappings

	// Only overwrite project page ID if value present or if projects data source has changed
	if defaultProjectPageID != nil || projectsDSUpdated {
		nc.DefaultProjectPageID = defaultProjectPageID
	}

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
	keys := []string{"project", "order", "done"}
	results := map[string]string{}

	for _, k := range keys {
		if k == "project" {
			// Find relation that matches the projects data source ID
			relProps := filterDataSourcePropertiesByType(ds.Properties, "relation")
			if len(relProps) == 0 {
				return nil, warnings, &clierr.Error{Code: clierr.NotFound, Message: "invalid tasks data source: no project relational property found", Details: map[string]any{"data_source_id": tasksDataSourceID}}
			}

			// Find all relation properties that point to the projects data source
			matching := []notion.DataSourceProperty{}
			for _, p := range relProps {
				if p.Relation.DataSourceID == projectsDataSourceID {
					matching = append(matching, p)
				}
			}

			if len(matching) == 1 {
				results[k] = matching[0].Name
			}

			if len(matching) > 1 {
				for _, m := range matching {
					if strings.ToLower(m.Name) == k {
						results[k] = m.Name
						break
					}
				}

				// More than one matching relation, ambiguously named
				if _, ok := results[k]; !ok {
					type candidate struct {
						Name string `json:"name"`
					}
					candidates := []candidate{}
					for _, m := range matching {
						candidates = append(candidates, candidate{Name: m.Name})
					}
					slices.SortFunc(candidates, func(a, b candidate) int {
						return cmp.Compare(a.Name, b.Name)
					})
					return nil, warnings, &clierr.Error{Code: clierr.AmbiguousSelector, Message: "invalid tasks data source: more than one relation points to the projects data source and none named \"Project\"", Details: map[string]any{"selector": tasksDataSourceID, "candidates": candidates}}
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
