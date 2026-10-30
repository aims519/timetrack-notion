package store

import (
	"context"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/google/uuid"
)

func (s *Store) StopTimer(ctx context.Context, stopUTC time.Time) (Entry, error) {
	tx, err := s.connectionPool.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "can't begin transaction", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = tx.Rollback() }()

	// Try stopping running entry, return ID if existed
	now := time.Now().UTC()
	stoppedID, err := tryStopRunningEntry(ctx, tx, stopUTC, now, s.dbPath)
	if err != nil {
		return Entry{}, err
	}
	if stoppedID == uuid.Nil {
		return Entry{}, &clierr.Error{Code: clierr.NoTimerRunning, Message: "no timer running"}
	}

	entry, err := scanEntryRow(ctx, tx, stoppedID)
	if err != nil {
		return Entry{}, err
	}

	err = tx.Commit()
	if err != nil {
		return Entry{}, &clierr.Error{Code: clierr.Internal, Message: "error committing transaction", Cause: err}
	}

	return entry, nil
}
