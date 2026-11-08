package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/google/uuid"
)

func (s *Store) AttachTaskToEntry(ctx context.Context, entryPrefix string, taskOrder *float64, taskID *string, all bool, nowUTC time.Time) (Entry, error) {
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
			AND tasks.context_id = ?1
			AND tasks.sort_order = ?2
			AND (?3 = 1 OR tasks.done = 0)
		`, DefaultContextID, *taskOrder, all).Scan(&foundTaskID)
		if errors.Is(err, sql.ErrNoRows) {
			return Entry{}, &clierr.Error{Code: clierr.NotFound, Message: "no matching tasks found for order number", Details: map[string]any{"selector": taskOrder}}
		}
		if err != nil {
			return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks by order: " + err.Error(), Cause: err, Details: map[string]any{"path": s.dbPath}}
		}
	} else if taskID != nil {
		err := tx.QueryRowContext(ctx, `
			SELECT page_id
			FROM tasks
			WHERE gone_at IS NULL
			AND tasks.context_id = ?1
			AND tasks.page_id = ?2
		`, DefaultContextID, *taskID).Scan(&foundTaskID)
		if errors.Is(err, sql.ErrNoRows) {
			return Entry{}, &clierr.Error{Code: clierr.NotFound, Message: "no matching tasks found", Details: map[string]any{"selector": taskID}}
		}
		if err != nil {
			return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks by page_id: " + err.Error(), Cause: err, Details: map[string]any{"path": s.dbPath}}
		}
	}

	if foundTaskID == "" {
		return Entry{}, &clierr.Error{Code: clierr.NotFound, Message: "no matching task found with given order/page_id", Details: map[string]any{"selector": taskID}}
	}

	entryID, err := resolveEntry(ctx, tx, entryPrefix, s.dbPath)
	if err != nil {
		return Entry{}, err
	}
	err = updateEntryWithTask(ctx, tx, foundTaskID, DefaultContextID, s.dbPath, entryID, nowUTC)
	if err != nil {
		return Entry{}, err
	}

	// Return Entry
	entry, err := scanEntryRow(ctx, tx, entryID)
	if err != nil {
		return entry, err
	}

	err = tx.Commit()
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error committing transaction", Cause: err}
	}

	return entry, nil
}

func resolveEntry(ctx context.Context, tx *sql.Tx, prefix, dbPath string) (uuid.UUID, error) {
	if prefix != "" {
		return entryByIDPrefix(ctx, tx, prefix, dbPath, DefaultContextID)
	}
	if id, _, found, err := findRunningEntry(ctx, tx, dbPath); err != nil || found {
		return id, err
	}
	if id, found, err := mostRecentEntry(ctx, tx, dbPath, DefaultContextID); err != nil || found {
		return id, err
	}

	return uuid.Nil, &clierr.Error{Code: clierr.NotFound, Message: "unable to find matching entry for task attachment"}
}

func entryByIDPrefix(ctx context.Context, tx *sql.Tx, prefix, dbPath, contextID string) (uuid.UUID, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM entries
		WHERE entries.context_id = ?1
		AND substr(id, 1, length(?2)) = ?2
		AND entries.deleted_at IS NULL
		ORDER BY start_utc DESC, id DESC
		LIMIT 2
	`, contextID, prefix)
	if err != nil {
		return uuid.Nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries by ID prefix: " + err.Error(), Cause: err, Details: map[string]any{"path": dbPath}}
	}
	defer func() { _ = rows.Close() }()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		err := rows.Scan(&id)
		if err != nil {
			return uuid.Nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries by ID prefix: " + err.Error(), Cause: err, Details: map[string]any{"path": dbPath}}
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries by ID prefix", Cause: err, Details: map[string]any{"path": dbPath}}
	}

	switch len(ids) {
	case 0:
		return uuid.Nil, &clierr.Error{Code: clierr.NotFound, Message: "unable to find matching entry with ID prefix", Details: map[string]any{"selector": prefix}}
	case 1:
		return ids[0], nil
	case 2:
		return uuid.Nil, &clierr.Error{Code: clierr.AmbiguousSelector, Message: "unable to find unique entry with ID prefix", Details: map[string]any{"selector": prefix, "candidates": ids}}
	default:
		return uuid.Nil, &clierr.Error{Code: clierr.AmbiguousSelector, Message: "unable to find unique entry with ID prefix", Details: map[string]any{"selector": prefix, "candidates": ids}}
	}
}

func mostRecentEntry(ctx context.Context, tx *sql.Tx, dbPath, contextID string) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := tx.QueryRowContext(ctx, `
		SELECT
		id
		FROM entries
		WHERE entries.context_id = ?1
		AND entries.deleted_at IS NULL
		ORDER BY start_utc DESC, id DESC
		LIMIT 1
	`, contextID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	if err != nil {
		return uuid.Nil, false, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query most recent entries: " + err.Error(), Cause: err, Details: map[string]any{"path": dbPath}}
	}

	return id, true, nil
}

func updateEntryWithTask(ctx context.Context, tx *sql.Tx, taskPageID, contextID, dbPath string, entryID uuid.UUID, nowUTC time.Time) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE entries
		SET task_page_id = ?1,
		sync_state = 'pending',
		updated_at = ?4
		WHERE entries.id = ?2
		AND entries.context_id = ?3
	`, taskPageID, entryID, contextID, ToDBString(nowUTC))
	if err != nil {
		return &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to update task: " + err.Error(), Cause: err, Details: map[string]any{"path": dbPath}}
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to update task by ID", Cause: err, Details: map[string]any{"path": dbPath}}
	}
	if rows != 1 {
		return &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to update task by ID", Details: map[string]any{"path": dbPath}}
	}

	return nil
}
