package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

type NotionContext struct {
	ID                   string
	TasksDataSourceID    *string
	ProjectsDataSourceID *string
	EntriesDataSourceID  *string
	PropertyMap          map[string]string
	DefaultProjectPageID *string
}

func (s *Store) NotionContext(ctx context.Context, contextID string) (NotionContext, error) {
	result := NotionContext{}
	var propMapStr *string
	err := s.connectionPool.QueryRowContext(ctx, `
		SELECT
		id,
		tasks_data_source_id,
		projects_data_source_id,
		entries_data_source_id,
		property_map,
		default_project_page_id
		FROM contexts
		WHERE id = ?
	`, contextID).Scan(&result.ID, &result.TasksDataSourceID, &result.ProjectsDataSourceID, &result.EntriesDataSourceID, &propMapStr, &result.DefaultProjectPageID)
	if errors.Is(err, sql.ErrNoRows) {
		return NotionContext{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "context not found", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	if err != nil {
		return NotionContext{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query database contexts", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}

	// TODO unmarshal propMapStr to result.PropertyMap
	return result, nil
}
