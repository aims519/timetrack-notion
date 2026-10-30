package store

import (
	"context"
	"database/sql"
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
