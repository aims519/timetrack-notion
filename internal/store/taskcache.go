package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// CacheState represents the current contents of the cache_state table
type CacheState struct {
	ContextID            string
	TasksRefreshedAt     *time.Time
	TasksRefreshFailedAt *time.Time
	TasksFullRefreshedAt *time.Time
	ProjectsRefreshedAt  *time.Time
}

func (s *Store) CacheState(ctx context.Context) (CacheState, error) {
	var (
		tasksRefreshedAtStr     *string
		tasksRefreshFailedAtStr *string
		tasksFullRefreshedAtStr *string
		projectsRefreshedAtStr  *string
	)
	result := CacheState{}
	err := s.connectionPool.QueryRowContext(ctx,
		`
			SELECT context_id, 
			tasks_refreshed_at, 
			tasks_refresh_failed_at, 
			tasks_full_refreshed_at, 
			projects_refreshed_at
			FROM cache_state
			WHERE context_id = ?
		`, DefaultContextID).Scan(&result.ContextID, &tasksRefreshedAtStr, &tasksRefreshFailedAtStr, &tasksFullRefreshedAtStr, &projectsRefreshedAtStr)
	if errors.Is(err, sql.ErrNoRows) {
		return CacheState{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "no cache_state found", Details: map[string]any{"path": s.dbPath}}
	}
	if err != nil {
		return CacheState{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query database cache_state", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}

	tRefreshedAt, err := FromNullableDBString(tasksRefreshedAtStr)
	if err != nil {
		return CacheState{}, &clierr.Error{Code: clierr.Internal, Message: "error parsing time string from tasks_refreshed_at", Cause: err, Details: map[string]any{"tasks_refreshed_at": tasksRefreshedAtStr}}
	}
	result.TasksRefreshedAt = tRefreshedAt

	tRefreshFailedAt, err := FromNullableDBString(tasksRefreshFailedAtStr)
	if err != nil {
		return CacheState{}, &clierr.Error{Code: clierr.Internal, Message: "error parsing time string from tasks_refresh_failed_at", Cause: err, Details: map[string]any{"tasks_refresh_failed_at": tasksRefreshFailedAtStr}}
	}
	result.TasksRefreshFailedAt = tRefreshFailedAt

	tFullRefreshAt, err := FromNullableDBString(tasksFullRefreshedAtStr)
	if err != nil {
		return CacheState{}, &clierr.Error{Code: clierr.Internal, Message: "error parsing time string from tasks_full_refreshed_at", Cause: err, Details: map[string]any{"tasks_full_refreshed_at": tasksFullRefreshedAtStr}}
	}
	result.TasksFullRefreshedAt = tFullRefreshAt

	tProjectsRefreshedAt, err := FromNullableDBString(projectsRefreshedAtStr)
	if err != nil {
		return CacheState{}, &clierr.Error{Code: clierr.Internal, Message: "error parsing time string from projects_refreshed_at", Cause: err, Details: map[string]any{"projects_refreshed_at": projectsRefreshedAtStr}}
	}
	result.ProjectsRefreshedAt = tProjectsRefreshedAt

	return result, nil
}

type Task struct {
	ContextID      string
	PageID         string
	Name           string
	ProjectPageID  *string
	SortOrder      *float64
	Done           bool
	LastEditedTime time.Time
	GoneAt         *time.Time
}

func (s *Store) RefreshTasks(ctx context.Context, tasks []Task, nowUTC time.Time, fullRefresh bool) error {
	tx, err := s.connectionPool.BeginTx(ctx, nil)
	if err != nil {
		return &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = tx.Rollback() }()

	upsertTaskStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO tasks(context_id, page_id, name, project_page_id, sort_order, done, last_edited_time)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(context_id, page_id)
		DO
			UPDATE SET name = excluded.name,
			project_page_id = excluded.project_page_id,
			sort_order = excluded.sort_order,
			done = excluded.done,
			last_edited_time = excluded.last_edited_time,
			gone_at = NULL
	`)
	if err != nil {
		return &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't prepare statement", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = upsertTaskStmt.Close() }()

	// All existing tasks are updated with gone_at = now and tasks that still exist in Notion reset
	// this back to NULL. Tasks that aren't reset are correctly marked as gone
	if fullRefresh {
		_, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET gone_at = ?
		WHERE context_id = ?
		AND gone_at IS NULL
	`, ToDBString(nowUTC), DefaultContextID)
		if err != nil {
			return &clierr.Error{Code: clierr.Internal, Message: "error updating cached task", Cause: err}
		}
	}

	for _, t := range tasks {
		_, err = upsertTaskStmt.ExecContext(ctx, t.ContextID, t.PageID, t.Name, t.ProjectPageID, t.SortOrder, t.Done, ToDBString(t.LastEditedTime))
		if err != nil {
			return &clierr.Error{Code: clierr.Internal, Message: "error writing cached task", Cause: err}
		}
	}

	// Update cache_state
	_, err = tx.ExecContext(ctx, `
		UPDATE cache_state
		SET tasks_refreshed_at = ?
		WHERE context_id = ?
	`, ToDBString(nowUTC), DefaultContextID)
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "error updating cache_state row", Cause: err}
	}

	if fullRefresh {
		_, err = tx.ExecContext(ctx, `
		UPDATE cache_state
		SET tasks_full_refreshed_at = ?
		WHERE context_id = ?
	`, ToDBString(nowUTC), DefaultContextID)
		if err != nil {
			return &clierr.Error{Code: clierr.Internal, Message: "error updating cache_state row", Cause: err}
		}
	}

	err = tx.Commit()
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "error committing transaction", Cause: err}
	}

	return nil
}

// RecordTasksRefreshFailed updates the cache_state.tasks_refresh_failed_at value with the current time,
// to be used for retry backoff in calls to Notion
func (s *Store) RecordTasksRefreshFailed(ctx context.Context, nowUTC time.Time) error {
	_, err := s.connectionPool.ExecContext(ctx, `
		UPDATE cache_state
		SET tasks_refresh_failed_at = ?
		WHERE context_id = ?
	`, ToDBString(nowUTC), DefaultContextID)
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "error updating cache_state row", Cause: err}
	}

	return nil
}
