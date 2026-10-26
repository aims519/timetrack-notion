package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/google/uuid"
)

func (s *Store) StartNewTimer(ctx context.Context, startUTC time.Time, description string) (Entry, error) {
	tx, err := s.connectionPool.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = tx.Rollback() }()

	runningEntryID, _, found, err := findRunningEntry(ctx, tx, s.dbPath)
	if err != nil {
		return Entry{}, err
	}
	if found {
		return Entry{}, &clierr.Error{Code: clierr.TimerRunning, Message: "timer already running", Details: map[string]any{"entry": runningEntryID.String()}}
	}

	// create new entry
	newID, err := insertNewEntry(ctx, tx, time.Now().UTC(), startUTC, description, s.dbPath)
	if err != nil {
		return Entry{}, err
	}

	// Return created entry details
	entry, err := scanEntryRow(ctx, tx, newID)
	if err != nil {
		return entry, err
	}

	err = tx.Commit()
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error committing transaction", Cause: err}
	}

	return entry, nil
}

func (s *Store) SwitchTimer(ctx context.Context, startUTC time.Time, description string) (started Entry, stopped *Entry, err error) {
	tx, err := s.connectionPool.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = tx.Rollback() }()

	// Try stopping running entry, return ID if existed
	now := time.Now().UTC()
	stoppedID, err := tryStopRunningEntry(ctx, tx, startUTC, now, s.dbPath)
	if err != nil {
		return Entry{}, nil, err
	}

	// Create new entry
	createdID, err := insertNewEntry(ctx, tx, now, startUTC, description, s.dbPath)
	if err != nil {
		return Entry{}, nil, err
	}

	// Scan to return stopped entry/new entry details for StartDoc
	startedEntry, err := scanEntryRow(ctx, tx, createdID)
	if err != nil {
		return Entry{}, nil, err
	}

	var stoppedEntryResult *Entry
	if stoppedID != uuid.Nil {
		found, err := scanEntryRow(ctx, tx, stoppedID)
		if err != nil {
			return Entry{}, nil, err
		}
		stoppedEntryResult = &found
	}

	err = tx.Commit()
	if err != nil {
		return Entry{}, nil, &clierr.Error{Code: clierr.Internal, Message: "error committing transaction", Cause: err}
	}

	return startedEntry, stoppedEntryResult, nil
}

func findRunningEntry(ctx context.Context, tx *sql.Tx, dbPath string) (id uuid.UUID, start time.Time, found bool, err error) {
	var runningEntryID uuid.UUID
	startUTCStr := ""
	err = tx.QueryRowContext(ctx, "SELECT id, start_utc FROM entries WHERE end_utc IS NULL AND deleted_at IS NULL LIMIT 1").Scan(&runningEntryID, &startUTCStr)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, time.Time{}, false, nil
	}

	if err != nil {
		return uuid.Nil, time.Time{}, false, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "query failed", Cause: err, Details: map[string]any{"path": dbPath}}
	}

	startUTC, err := FromDBString(startUTCStr)
	if err != nil {
		return uuid.Nil, time.Time{}, false, &clierr.Error{Code: clierr.Internal, Message: "unable to parse time from start_utc", Cause: err, Details: map[string]any{"start_utc": startUTCStr}}
	}

	return runningEntryID, startUTC, true, err
}

func tryStopRunningEntry(ctx context.Context, tx *sql.Tx, endUTC, nowUTC time.Time, dbPath string) (uuid.UUID, error) {
	runningEntryID, runningEntryStart, found, err := findRunningEntry(ctx, tx, dbPath)
	if err != nil {
		return uuid.Nil, err
	}

	if !found {
		return uuid.Nil, nil
	}

	if !endUTC.After(runningEntryStart) {
		return uuid.Nil, &clierr.Error{Code: clierr.InvalidTime, Message: "attempted to stop running entry before its start", Details: map[string]any{"input": endUTC, "start": runningEntryStart}}
	}

	endUTCFormatted := ToDBString(endUTC)
	nowUTCFormatted := ToDBString(nowUTC)
	result, err := tx.ExecContext(ctx, `
		UPDATE entries
		SET end_utc = ?,
		updated_at = ?
		WHERE id = ?`, endUTCFormatted, nowUTCFormatted, runningEntryID)
	if err != nil {
		return uuid.Nil, &clierr.Error{Code: clierr.Internal, Message: "error stopping running entry", Cause: err}
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return uuid.Nil, &clierr.Error{Code: clierr.Internal, Message: "error stopping running entry", Cause: err}
	}
	if rows != 1 {
		return uuid.Nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "running entry not updated", Details: map[string]any{"path": dbPath}}
	}

	return runningEntryID, nil
}

func insertNewEntry(ctx context.Context, tx *sql.Tx, nowUTC, startUTC time.Time, description string, dbPath string) (uuid.UUID, error) {
	newID := uuid.New()
	updatedAtUTCFormatted := ToDBString(nowUTC)
	startUTCFormatted := ToDBString(startUTC)

	result, err := tx.ExecContext(ctx, `
	INSERT INTO entries(id, context_id, start_utc, description, updated_at)
	SELECT
	? AS id,
	contexts.id AS context_id,
	? AS start_utc,
	? AS description,
	? AS updated_at
	FROM contexts WHERE id = '00000000-0000-0000-0000-000000000000';
	`, newID, startUTCFormatted, description, updatedAtUTCFormatted)
	if err != nil {
		return uuid.Nil, &clierr.Error{Code: clierr.Internal, Message: "error writing new entry", Cause: err}
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return uuid.Nil, &clierr.Error{Code: clierr.Internal, Message: "error writing new entry", Cause: err}
	}
	if rows != 1 {
		return uuid.Nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "no context found in the database", Details: map[string]any{"path": dbPath}}
	}

	return newID, nil
}

func scanEntryRow(ctx context.Context, tx *sql.Tx, id uuid.UUID) (Entry, error) {
	var entry Entry
	var startUTCStr string
	var endUTCStr *string
	var updatedAtUTCStr string
	err := tx.QueryRowContext(ctx, `
	SELECT id,
	description,
	start_utc,
	end_utc,
	task_page_id,
	entry_page_id,
	sync_state,
	updated_at
	FROM entries 
	WHERE id = ?`, id).Scan(&entry.ID,
		&entry.Description,
		&startUTCStr,
		&endUTCStr,
		&entry.TaskPageID,
		&entry.EntryPageID,
		&entry.SyncState,
		&updatedAtUTCStr,
	)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: fmt.Sprintf("error reading newly created row from entries: %v", err), Cause: err}
	}

	entry.StartUTC, err = FromDBString(startUTCStr)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error parsing time string from start_utc", Cause: err, Details: map[string]any{"start_utc": startUTCStr}}
	}

	entry.EndUTC, err = FromNullableDBString(endUTCStr)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error parsing time string from end_utc", Cause: err, Details: map[string]any{"end_utc": endUTCStr}}
	}

	entry.UpdatedAt, err = FromDBString(updatedAtUTCStr)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error parsing time string from updated_at", Cause: err, Details: map[string]any{"updated_at": updatedAtUTCStr}}
	}

	return entry, nil
}
