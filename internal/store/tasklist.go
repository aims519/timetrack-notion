package store

import (
	"context"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// NoLimit is the value recognised by the list script as none, so that all results are returned
const NoLimit = 0

// ListTasks returns all cached Notion tasks from the database subject to
//   - limit: only return the top n results
//   - all: include tasks marked as done in the results
func (s *Store) ListTasks(ctx context.Context, limit int, all bool, projectID *string) (tasks []Task, truncated bool, err error) {
	var queryLimit int
	if limit == NoLimit {
		queryLimit = -1
	} else {
		queryLimit = limit + 1
	}

	var results []Task
	rows, err := s.connectionPool.QueryContext(ctx, `
		SELECT
		tasks.context_id,
		tasks.page_id,
		tasks.name,
		project_page_id,
		projects.name,
		sort_order,
		done
		FROM tasks
		JOIN contexts ON tasks.context_id = contexts.id
		LEFT JOIN projects ON tasks.project_page_id = projects.page_id
			AND projects.context_id = tasks.context_id
			AND projects.gone_at IS NULL
		WHERE contexts.id = ?1
		AND (?2 OR tasks.done = 0)
		AND tasks.gone_at IS NULL
		AND (COALESCE(?3, contexts.default_project_page_id) IS NULL OR tasks.project_page_id = COALESCE(?3, contexts.default_project_page_id))
		ORDER BY sort_order NULLS LAST, tasks.name COLLATE NOCASE
		LIMIT ?4
	`, DefaultContextID, all, projectID, queryLimit)
	if err != nil {
		return nil, false, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks: " + err.Error(), Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		if limit != NoLimit && len(results) == limit {
			truncated = true // more rows exist but omitted
			break
		}

		t, err := scanTask(rows)
		if err != nil {
			return nil, false, err
		}

		results = append(results, t)
	}

	if err := rows.Err(); err != nil {
		return nil, false, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}

	return results, truncated, nil
}

func scanTask(s scanner) (Task, error) {
	var t Task
	err := s.Scan(&t.ContextID, &t.PageID, &t.Name, &t.ProjectPageID, &t.ProjectName, &t.SortOrder, &t.Done)

	return t, err
}

func (s *Store) AllTaskIDs(ctx context.Context) ([]string, error) {
	rows, err := s.connectionPool.QueryContext(ctx, `
		SELECT page_id
		FROM tasks
		WHERE context_id = ?
	`, DefaultContextID)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks: " + err.Error(), Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		err = rows.Scan(&id)
		if err != nil {
			return nil, err
		}

		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}

	return ids, nil
}
