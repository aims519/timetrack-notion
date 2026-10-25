package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/google/uuid"
)

func TestStartNewTimer_NewEntry(t *testing.T) {
	tests := []struct {
		name        string
		startUTC    time.Time
		description string
		wantStored  string
	}{
		{"utc_start", time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC), "writing tests", "2026-10-19T09:00:00.000000000Z"},
		{"nanoseconds_kept", time.Date(2026, 10, 19, 9, 0, 0, 123456789, time.UTC), "writing tests", "2026-10-19T09:00:00.123456789Z"},
		{"non_utc_start_stored_as_utc", time.Date(2026, 10, 19, 10, 0, 0, 0, time.FixedZone("plus1", 60*60)), "writing tests", "2026-10-19T09:00:00.000000000Z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			before := time.Now()
			got, err := st.StartNewTimer(t.Context(), tc.startUTC, tc.description)
			after := time.Now()
			if err != nil {
				t.Fatalf("StartNewTimer() want nil error, got %v", err)
			}

			// Returned entry
			if _, err := uuid.Parse(got.ID); err != nil {
				t.Errorf("ID want a UUID, got %q: %v", got.ID, err)
			}
			if got.Description == nil || *got.Description != tc.description {
				t.Errorf("Description want %q, got %v", tc.description, got.Description)
			}
			if !got.StartUTC.Equal(tc.startUTC) {
				t.Errorf("StartUTC want %v, got %v", tc.startUTC, got.StartUTC)
			}
			if got.EndUTC != nil {
				t.Errorf("EndUTC want nil, got %v", *got.EndUTC)
			}
			if got.TaskPageID != nil {
				t.Errorf("TaskPageID want nil, got %q", *got.TaskPageID)
			}
			if got.EntryPageID != nil {
				t.Errorf("EntryPageID want nil, got %q", *got.EntryPageID)
			}
			if got.SyncState != "pending" {
				t.Errorf("SyncState want %q, got %q", "pending", got.SyncState)
			}
			if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) {
				t.Errorf("UpdatedAt want between %v and %v, got %v", before, after, got.UpdatedAt)
			}

			// Committed row
			if n := countRows(t, st.connectionPool, "entries"); n != 1 {
				t.Fatalf("entries want 1 row, got %d", n)
			}
			var storedStart string
			err = st.connectionPool.QueryRowContext(t.Context(), "SELECT start_utc FROM entries WHERE id = ?", got.ID).Scan(&storedStart)
			if err != nil {
				t.Fatalf("scan failed: %v", err)
			}
			if storedStart != tc.wantStored {
				t.Errorf("start_utc want %q, got %q", tc.wantStored, storedStart)
			}
		})
	}
}

func TestStartNewTimer_MissingContext(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st := openTestStore(t, dbPath)

	// The first migration always creates the context, so it can only be missing if something
	// outside tt changed the database. The insert selects its context_id from contexts, so
	// with no context it inserts nothing
	_, err := st.connectionPool.ExecContext(t.Context(), "DELETE FROM contexts WHERE id = ?", testContextID)
	if err != nil {
		t.Fatalf("delete context failed: %v", err)
	}

	got, err := st.StartNewTimer(t.Context(), time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC), "writing tests")

	var errcli *clierr.Error
	if !errors.As(err, &errcli) {
		t.Fatalf("want *clierr.Error, got %T: %v", err, err)
	}
	if errcli.Code != clierr.DatabaseUnusable {
		t.Errorf("Code want %q, got %q", clierr.DatabaseUnusable, errcli.Code)
	}
	if got := errcli.Details["path"]; got != dbPath {
		t.Errorf("Details[\"path\"] want %q, got %v", dbPath, got)
	}
	if got.ID != "" {
		t.Errorf("ID want empty on error, got %q", got.ID)
	}
	if n := countRows(t, st.connectionPool, "entries"); n != 0 {
		t.Errorf("entries want 0 rows, got %d", n)
	}
}

func TestStartNewTimer_ExistingEntries(t *testing.T) {
	const insert = "INSERT INTO entries (id, context_id, start_utc, end_utc, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?)"
	const start = "2026-10-19T09:00:00.000000000Z"
	const end = "2026-10-19T10:00:00.000000000Z"

	type entry struct {
		id             string
		end, deletedAt any
	}

	tests := []struct {
		name     string
		existing entry
		wantCode clierr.ErrorCode // empty means the start must succeed
		wantRows int
	}{
		{name: "running", existing: entry{id: "a"}, wantCode: clierr.TimerRunning, wantRows: 1},
		{name: "stopped", existing: entry{id: "a", end: end}, wantRows: 2},
		{name: "deleted_running", existing: entry{id: "a", deletedAt: end}, wantRows: 2},
		{name: "deleted_stopped", existing: entry{id: "a", end: end, deletedAt: end}, wantRows: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			_, err := st.connectionPool.ExecContext(t.Context(), insert,
				tc.existing.id, testContextID, start, tc.existing.end, start, tc.existing.deletedAt)
			if err != nil {
				t.Fatalf("existing entry %q: insert failed: %v", tc.existing.id, err)
			}

			got, err := st.StartNewTimer(t.Context(), time.Date(2026, 10, 19, 11, 0, 0, 0, time.UTC), "writing tests")

			if tc.wantCode == "" {
				if err != nil {
					t.Errorf("StartNewTimer() want nil error, got %v", err)
				}
				if got.ID == "" || got.ID == tc.existing.id {
					t.Errorf("ID want a new entry ID, got %q", got.ID)
				}
			} else {
				var errcli *clierr.Error
				if !errors.As(err, &errcli) {
					t.Fatalf("want *clierr.Error, got %T: %v", err, err)
				}
				if errcli.Code != tc.wantCode {
					t.Errorf("Code want %q, got %q", tc.wantCode, errcli.Code)
				}
				if entryID := errcli.Details["entry"]; entryID != tc.existing.id {
					t.Errorf("Details[\"entry\"] want %q, got %v", tc.existing.id, entryID)
				}
			}

			if n := countRows(t, st.connectionPool, "entries"); n != tc.wantRows {
				t.Errorf("entries want %d rows, got %d", tc.wantRows, n)
			}
		})
	}
}
