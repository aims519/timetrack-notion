package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/google/uuid"
)

// DefaultContextID is the only context in v1; more than one context is deferred
const DefaultContextID = "00000000-0000-0000-0000-000000000000"

// tryStopRunningEntry stops the running entry at endUTC, if there is one, and returns its ID.
// It returns uuid.Nil when no entry is running, and invalid_time if endUTC isn't after its start
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

// scanner is the Scan method shared by *sql.Row and *sql.Rows, so scanEntry can read either
type scanner interface {
	Scan(dest ...any) error
}

// scanEntryRow reads the entry with id, with its task name, inside tx
func scanEntryRow(ctx context.Context, tx *sql.Tx, id uuid.UUID) (Entry, error) {
	row := tx.QueryRowContext(ctx, `
	SELECT id,
	description,
	start_utc,
	end_utc,
	task_page_id,
	tasks.name,
	entry_page_id,
	sync_state,
	updated_at,
	deleted_at
	FROM entries 
	LEFT JOIN tasks ON entries.task_page_id = tasks.page_id
		AND tasks.context_id = entries.context_id
	WHERE id = ?`, id)

	return scanEntry(row)
}

// scanEntry reads one entry from a row selected with the columns scanEntryRow uses
func scanEntry(s scanner) (Entry, error) {
	var entry Entry
	var startUTCStr string
	var endUTCStr *string
	var updatedAtUTCStr string
	var deletedAtUTCStr *string
	err := s.Scan(&entry.ID,
		&entry.Description,
		&startUTCStr,
		&endUTCStr,
		&entry.TaskPageID,
		&entry.TaskName,
		&entry.EntryPageID,
		&entry.SyncState,
		&updatedAtUTCStr,
		&deletedAtUTCStr,
	)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "unable to scan row from entries: " + err.Error(), Cause: err}
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

	entry.DeletedAt, err = FromNullableDBString(deletedAtUTCStr)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error parsing time string from deleted_at", Cause: err, Details: map[string]any{"deleted_at": deletedAtUTCStr}}
	}

	return entry, nil
}

// findRunningEntry returns the entry that has no end time, if there is one
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

// SanitiseID removes dashes before saving an ID to the store
func SanitiseID(id string) string {
	return strings.ReplaceAll(id, "-", "")
}
