package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

type CountSummary struct {
	Pending int
	Blocked int
	Waiting int
}

type Status struct {
	Running      *Entry
	Connected    bool
	TodayEntries []Entry
	CountSummary CountSummary
}

func (s *Store) ReadStatus(ctx context.Context, todayStartUTC, todayEndUTC time.Time) (Status, error) {
	tx, err := s.connectionPool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Status{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = tx.Rollback() }()

	// check for running entry
	var running *Entry
	runningID, _, found, err := findRunningEntry(ctx, tx, s.dbPath)
	if err != nil {
		return Status{}, err
	}
	if found {
		entry, err := scanEntryRow(ctx, tx, runningID)
		if err != nil {
			return Status{}, err
		}
		running = &entry
	}

	// context connected?
	connected, err := contextConnected(ctx, tx, s.dbPath)
	if err != nil {
		return Status{}, err
	}

	// get entries started today (>= todayStartUTC && < todayEndUTC)
	todayEntries, err := entriesStartedToday(ctx, tx, todayStartUTC, todayEndUTC, s.dbPath)
	if err != nil {
		return Status{}, err
	}

	// get count summary
	summary, err := countSummary(ctx, tx, s.dbPath)
	if err != nil {
		return Status{}, err
	}

	result := Status{
		Running:      running,
		Connected:    connected,
		TodayEntries: todayEntries,
		CountSummary: summary,
	}

	return result, nil
}

// contextConnected checks if the default context has the Notion tasks_db_id set, and returns true if so
func contextConnected(ctx context.Context, tx *sql.Tx, dbPath string) (bool, error) {
	var tasksDBID *string
	err := tx.QueryRowContext(ctx, "SELECT tasks_db_id from contexts WHERE id = ?", defaultContextID).Scan(&tasksDBID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "no context found", Details: map[string]any{"path": dbPath}}
	}
	if err != nil {
		return false, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query database context", Cause: err, Details: map[string]any{"path": dbPath}}
	}

	return tasksDBID != nil, nil
}

// entriesStartedToday returns all entries that were started during the provided start and end times
func entriesStartedToday(ctx context.Context, tx *sql.Tx, start, end time.Time, dbPath string) ([]Entry, error) {
	startFormatted := ToDBString(start)
	endFormatted := ToDBString(end)

	rows, err := tx.QueryContext(ctx, `
    SELECT id,
	description,
	start_utc,
	end_utc,
	task_page_id,
	entry_page_id,
	sync_state,
	updated_at
	FROM entries
	WHERE start_utc >= ? 
	AND start_utc < ?
	AND deleted_at IS NULL
	ORDER BY start_utc
	`, startFormatted, endFormatted)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries started today", Cause: err, Details: map[string]any{"path": dbPath}}
	}
	defer func() { _ = rows.Close() }()

	var entries []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}

		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries started today", Cause: err, Details: map[string]any{"path": dbPath}}
	}

	return entries, nil
}

// countSummary queries all entries in the database and returns counts for
//
//   - pending: everything needed for sync is present, but hasn't yet been synced
//   - blocked: a sync was attempted but failed permanently (something needs fixing)
//   - waiting: can't be synced because of missing information, e.g. task not attached
func countSummary(ctx context.Context, tx *sql.Tx, dbPath string) (CountSummary, error) {
	var pendingCount, blockedCount, waitingCount int
	err := tx.QueryRowContext(ctx, `
	SELECT
		COALESCE(SUM(CASE WHEN entries.sync_state IN ('pending', 'failed')
							AND entries.end_utc IS NOT NULL
							AND entries.task_page_id IS NOT NULL
			THEN 1 ELSE 0 END), 0) AS pendingCount,
		COALESCE(SUM(CASE WHEN entries.sync_state == 'blocked' THEN 1 ELSE 0 END), 0) AS blockedCount,
		COALESCE(SUM(CASE WHEN entries.sync_state == 'pending'
		                    AND entries.deleted_at IS NULL
		                    AND (entries.task_page_id IS NULL OR entries.end_utc IS NULL)
		    THEN 1 ELSE 0 END), 0) AS waitingCount
	FROM entries
	`).Scan(&pendingCount, &blockedCount, &waitingCount)

	if err != nil {
		return CountSummary{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries", Cause: err, Details: map[string]any{"path": dbPath}}
	}

	return CountSummary{Pending: pendingCount, Waiting: waitingCount, Blocked: blockedCount}, nil
}
