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
		`, defaultContextID).Scan(&result.ContextID, &tasksRefreshedAtStr, &tasksRefreshFailedAtStr, &tasksFullRefreshedAtStr, &projectsRefreshedAtStr)
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
