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
