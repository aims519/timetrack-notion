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
		return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err}
	}
	defer func() { _ = tx.Rollback() }()

	// Check for running timer
	runningEntryID := ""
	err = tx.QueryRowContext(ctx, "SELECT id FROM entries WHERE end_utc IS NULL AND deleted_at IS NULL LIMIT 1").Scan(&runningEntryID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "query failed", Cause: err}
	}
	if runningEntryID != "" {
		return Entry{}, &clierr.Error{Code: clierr.TimerRunning, Message: "timer already running", Details: map[string]any{"entry": runningEntryID}}
	}

	// create new entry
	newID := uuid.New()
	updatedAtUTCFormatted := ToDBString(time.Now().UTC())
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
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error writing new entry", Cause: err}
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error writing new entry"}
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

func (s *Store) SwitchTimer(ctx context.Context, startUTC time.Time, description string) error {
	return nil
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
