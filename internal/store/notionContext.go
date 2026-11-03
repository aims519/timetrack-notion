package store

import (
	"context"
	"database/sql"
	"encoding/json"
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

	if propMapStr != nil {
		err := json.Unmarshal([]byte(*propMapStr), &result.PropertyMap)
		if err != nil {
			return NotionContext{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to unmarshal returned property_map string", Cause: err, Details: map[string]any{"path": s.dbPath, "property_map": propMapStr}}
		}
	}
	return result, nil
}

func (s *Store) SaveNotionContext(ctx context.Context, c NotionContext) error {
	var propMapStr *string
	if c.PropertyMap != nil {
		b, err := json.Marshal(c.PropertyMap)
		if err != nil {
			return &clierr.Error{Code: clierr.Internal, Message: "unable to marshal property_map JSON", Cause: err}
		}
		propMap := string(b)
		propMapStr = &propMap
	}

	res, err := s.connectionPool.ExecContext(ctx, `
		UPDATE contexts
		SET
		tasks_data_source_id = ?,
		projects_data_source_id = ?,
		entries_data_source_id = ?,
		property_map = ?,
		default_project_page_id = ?
		WHERE id = ?
	`, c.TasksDataSourceID, c.ProjectsDataSourceID, c.EntriesDataSourceID, propMapStr, c.DefaultProjectPageID, c.ID)
	if err != nil {
		return &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to save Notion database context", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}

	if rows, err := res.RowsAffected(); err != nil || rows != 1 {
		return &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to save Notion database context details", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}

	return nil
}
