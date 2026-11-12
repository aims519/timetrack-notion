package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/google/uuid"
)

// AttachTaskToEntry attaches a task to an entry using the provided taskPageID
func (s *Store) AttachTaskToEntry(ctx context.Context, entryPrefix string, taskPageID string, nowUTC time.Time) (Entry, error) {
	tx, err := s.connectionPool.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = tx.Rollback() }()

	// Lookup entry
	entryID, err := resolveEntry(ctx, tx, entryPrefix, s.dbPath)
	if err != nil {
		return Entry{}, err
	}

	// Execute task attach in DB
	err = updateEntryWithTask(ctx, tx, taskPageID, DefaultContextID, s.dbPath, entryID, nowUTC)
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

// resolveEntry attempts to look up an entry first by prefix if provided, then attempts
// to find the currently running entry, and finally the most recent entry
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

// entryByIDPrefix attempts to look up an entry using an ID prefix
func entryByIDPrefix(ctx context.Context, tx *sql.Tx, prefix, dbPath, contextID string) (uuid.UUID, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id,
		description,
		start_utc,
		end_utc,
		task_page_id,
		tasks.name,
		entries.entry_page_id,
		entries.sync_state,
		entries.updated_at,
		entries.deleted_at
		FROM entries
		LEFT JOIN tasks ON entries.task_page_id = tasks.page_id
			AND tasks.context_id = entries.context_id
		WHERE entries.context_id = ?1
		AND substr(id, 1, length(?2)) = ?2
		AND entries.deleted_at IS NULL
		ORDER BY start_utc DESC, id DESC
	`, contextID, prefix)
	if err != nil {
		return uuid.Nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries by ID prefix: " + err.Error(), Cause: err, Details: map[string]any{"path": dbPath}}
	}
	defer func() { _ = rows.Close() }()

	var candidates []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return uuid.Nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries by ID prefix: " + err.Error(), Cause: err, Details: map[string]any{"path": dbPath}}
		}
		candidates = append(candidates, e)
	}
	if err := rows.Err(); err != nil {
		return uuid.Nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries by ID prefix", Cause: err, Details: map[string]any{"path": dbPath}}
	}

	switch len(candidates) {
	case 0:
		return uuid.Nil, &clierr.Error{Code: clierr.NotFound, Message: "unable to find matching entry with ID prefix", Details: map[string]any{"selector": prefix}}
	case 1:
		entryID, err := uuid.Parse(candidates[0].ID)
		if err != nil {
			return uuid.Nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to parse entry ID in lookup", Cause: err, Details: map[string]any{"path": dbPath}}
		}
		return entryID, nil
	default:
		return uuid.Nil, &AmbiguousEntryError{Candidates: candidates}
	}
}

// mostRecentEntry attempts to look up the most recently started entry in the event a running
// entry was not found
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

// lookupTask attempts to look up a task by its order number if that's the selector the user provided.
// Otherwise, by ID
func lookupTask(ctx context.Context, tx *sql.Tx, task TaskSelector, dbPath string) (string, error) {
	if task.Order != nil {
		return taskByOrderNumber(ctx, tx, *task.Order, task.All, dbPath)
	} else if task.PageID != nil {
		return taskByID(ctx, tx, *task.PageID, dbPath)
	}

	return "", ErrTaskNotFound
}

// taskByID looks up a task by its ID
func taskByID(ctx context.Context, tx *sql.Tx, taskID, dbPath string) (string, error) {
	var foundTaskID string
	err := tx.QueryRowContext(ctx, `
			SELECT page_id
			FROM tasks
			WHERE gone_at IS NULL
			AND tasks.context_id = ?1
			AND tasks.page_id = ?2
		`, DefaultContextID, taskID).Scan(&foundTaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrTaskNotFound
	}
	if err != nil {
		return "", &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks by page_id: " + err.Error(), Cause: err, Details: map[string]any{"path": dbPath}}
	}

	return foundTaskID, nil
}

// taskByOrderNumber attempts to find a unique task matching the given order number
func taskByOrderNumber(ctx context.Context, tx *sql.Tx, order float64, all bool, dbPath string) (string, error) {
	rows, err := tx.QueryContext(ctx, `
			SELECT 
			tasks.context_id,
			tasks.page_id, 
			tasks.name,
			tasks.project_page_id,
			projects.name,
			tasks.sort_order,
			tasks.done
			FROM tasks
			JOIN contexts ON tasks.context_id = contexts.id
			LEFT JOIN projects ON tasks.project_page_id = projects.page_id
				AND projects.gone_at IS NULL
				AND projects.context_id = contexts.id
			WHERE tasks.gone_at IS NULL
			AND tasks.context_id = ?1
			AND tasks.sort_order = ?2
			AND (?3 = 1 OR tasks.done = 0)
			AND (contexts.default_project_page_id IS NULL OR (tasks.project_page_id = contexts.default_project_page_id))
			ORDER BY tasks.sort_order, tasks.name 
		`, DefaultContextID, order, all)
	if err != nil {
		return "", &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks by order number: " + err.Error(), Cause: err, Details: map[string]any{"path": dbPath}}
	}
	defer func() { _ = rows.Close() }()

	var candidates []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return "", &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks by order number: " + err.Error(), Cause: err, Details: map[string]any{"path": dbPath}}
		}
		candidates = append(candidates, t)
	}
	if err := rows.Err(); err != nil {
		return "", &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query cached tasks by order number", Cause: err, Details: map[string]any{"path": dbPath}}
	}

	switch len(candidates) {
	case 0:
		return "", ErrTaskNotFound
	case 1:
		return candidates[0].PageID, nil
	default:
		return "", &AmbiguousTaskError{Candidates: candidates}
	}
}

// updateEntryWithTask updates the target entry row with an attached task, and marks
// that entry as pending for sync
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

// TaskSelector is a representation of a user-provided task selector, which
// can either be an order number or page ID. The all boolean determines whether
// tasks marked as 'done' are allowed
type TaskSelector struct {
	Order  *float64
	PageID *string
	All    bool
}

// hasValue returns true if either the order number
// or pageID is not nil
func (ts TaskSelector) hasValue() bool {
	return ts.Order != nil || ts.PageID != nil
}

// ResolveTask attempts to retrieve a task pageID that matches the user's provided
// selector. Should be called prior to any write/update queries for store entries where an optional
// task pageID is required
func (s *Store) ResolveTask(ctx context.Context, selector TaskSelector) (*string, error) {
	if !selector.hasValue() {
		return nil, nil
	}
	tx, err := s.connectionPool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = tx.Rollback() }()

	foundTaskID, err := lookupTask(ctx, tx, selector, s.dbPath)
	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, &clierr.Error{Code: clierr.Internal, Message: "error committing transaction", Cause: err}
	}

	return &foundTaskID, nil
}
