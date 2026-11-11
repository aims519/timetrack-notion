package store

import (
	"context"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// PendingEntries returns all entry rows that are pending sync to Notion
func (s *Store) PendingEntries(ctx context.Context) ([]Entry, error) {
	rows, err := s.connectionPool.QueryContext(ctx, `
		SELECT
		id,
		description,
		start_utc,
		end_utc,
		task_page_id,
		tasks.name,
		entry_page_id,
		sync_state,
		updated_at
		FROM entries
		JOIN tasks ON entries.task_page_id = tasks.page_id
		WHERE entries.sync_state IN ('pending', 'failed')
		AND entries.end_utc IS NOT NULL
	`)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries: " + err.Error(), Cause: err, Details: map[string]any{"path": s.dbPath}}
	}
	defer func() { _ = rows.Close() }()

	var results []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, e)
	}

	if err := rows.Err(); err != nil {
		return nil, &clierr.Error{Code: clierr.DatabaseUnusable, Message: "unable to query entries", Cause: err, Details: map[string]any{"path": s.dbPath}}
	}

	return results, nil
}

// SyncAttempt sets the create_attempted_at value of an entry row
func (s *Store) SyncAttempt(ctx context.Context, entryID string, attemptedAtUTC time.Time) error {
	res, err := s.connectionPool.ExecContext(ctx, `
		UPDATE entries
		SET create_attempted_at = ?2
		WHERE id = ?1
	`, entryID, ToDBString(attemptedAtUTC))
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "error updating entries row", Cause: err}
	}
	if count, _ := res.RowsAffected(); count != 1 {
		return &clierr.Error{Code: clierr.Usage, Message: "no matching entry found for given ID", Details: map[string]any{"id": entryID}}
	}

	return nil
}

// CompleteSync updates an entry row's sync status following an attempt
func (s *Store) CompleteSync(ctx context.Context, entryID, syncState string, syncError, entryPageID *string, attemptedAtUTC time.Time, syncedAt *time.Time) error {
	var syncedAtFormatted *string
	if syncedAt != nil {
		syncedAtStr := ToDBString(*syncedAt)
		syncedAtFormatted = &syncedAtStr
	}
	_, err := s.connectionPool.ExecContext(ctx, `
		UPDATE entries
		SET entry_page_id = ?1,
		sync_state = ?2,
		sync_error = ?3,
		synced_at = ?4,
		updated_at = ?5
		WHERE id = ?6
		AND (updated_at < ?5) -- entries updated since the sync began should remain pending
	`, entryPageID, syncState, syncError, syncedAtFormatted, ToDBString(attemptedAtUTC), entryID)
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "error updating entries row", Cause: err}
	}

	return nil
}
