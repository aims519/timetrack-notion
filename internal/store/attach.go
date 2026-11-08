package store

import (
	"context"
	"github.com/aims519/timetrack-notion/internal/clierr"
)

func (s *Store) AttachTaskToEntry(ctx context.Context, taskOrder *float64, taskID *string) (Entry, error) {
	tx, err := s.connectionPool.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = tx.Rollback() }()

	// Lookup task
	var foundTaskID string
	if taskOrder != nil {
		err := tx.QueryRowContext(ctx, `
			SELECT page_id
			FROM tasks
			WHERE gone_at IS NULL
			AND tasks.context_id = ?
			AND tasks.sort_order = ?
		`, DefaultContextID, *taskOrder).Scan(&foundTaskID)
		if err != nil {
			return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks by order: " + err.Error(), Cause: err, Details: map[string]any{"path": s.dbPath}}
		}
	} else if taskID != nil {
		err := tx.QueryRowContext(ctx, `
			SELECT page_id
			FROM tasks
			WHERE gone_at IS NULL
			AND tasks.context_id = ?
			AND tasks.page_id = ?
		`, DefaultContextID, *taskID).Scan(&foundTaskID)
		if err != nil {
			return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks by page_id: " + err.Error(), Cause: err, Details: map[string]any{"path": s.dbPath}}
		}
	}

	if foundTaskID == "" {
		return Entry{}, &clierr.Error{Code: clierr.NotFound, Message: "no matching task found with given order/page_id", Details: map[string]any{"selector": taskID}}
	}

	// Update running Entry
	runningEntryID, _, found, err := findRunningEntry(ctx, tx, s.dbPath)
	if !found {
		return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to find running entry to attach", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE entries
		SET task_page_id = ?
		WHERE entries.id = ?
		AND entries.context_id = ?
	`, foundTaskID, runningEntryID, DefaultContextID)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks: " + err.Error(), Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	rows, err := res.RowsAffected()
	if rows != 1 {
		return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to attach task to entry", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}

	// Return Entry
	entry, err := scanEntryRow(ctx, tx, runningEntryID)
	if err != nil {
		return entry, err
	}

	err = tx.Commit()
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error committing transaction", Cause: err}
	}

	return entry, nil
}
