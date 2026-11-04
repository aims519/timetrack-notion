package store

import (
	"context"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

type Project struct {
	ContextID string
	PageID    string
	Name      string
	GoneAt    *time.Time
}

func (s *Store) RefreshProjects(ctx context.Context, projects []Project, nowUTC time.Time) (goneCount *int, err error) {
	tx, err := s.connectionPool.BeginTx(ctx, nil)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = tx.Rollback() }()

	// Set all projects to gone, to be overwritten by projects that still exist
	_, err = tx.ExecContext(ctx, `
		UPDATE projects
		SET gone_at = ?
		WHERE context_id = ? AND gone_at IS NULL
	`, ToDBString(nowUTC), DefaultContextID)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.Internal, Message: "error updating cached projects", Cause: err}
	}

	upsertStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO projects(context_id, page_id, name)
		VALUES (?, ?, ?)
		ON CONFLICT (context_id, page_id)
		DO
			UPDATE SET name = excluded.name,
			gone_at = NULL
	`)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't prepare statement", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = upsertStmt.Close() }()

	for _, p := range projects {
		_, err := upsertStmt.ExecContext(ctx, p.ContextID, p.PageID, p.Name)
		if err != nil {
			return nil, &clierr.Error{Code: clierr.Internal, Message: "error writing cached project", Cause: err}
		}
	}

	// Update cache_state
	_, err = tx.ExecContext(ctx, `
		UPDATE cache_state
		SET projects_refreshed_at = ?
		WHERE context_id = ?
	`, ToDBString(nowUTC), DefaultContextID)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.Internal, Message: "error updating cache_state row", Cause: err}
	}

	// return number of projects marked as gone during this refresh
	err = tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM projects
			WHERE gone_at = ?
			AND projects.context_id = ?
		`, ToDBString(nowUTC), DefaultContextID).Scan(&goneCount)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.Internal, Message: "error querying projects gone count", Cause: err}
	}

	err = tx.Commit()
	if err != nil {
		return nil, &clierr.Error{Code: clierr.Internal, Message: "error committing transaction", Cause: err}
	}

	return goneCount, nil
}
