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
	// The running entry's ID is scanned into a uuid.UUID, so it must be one
	const existingID = "11111111-1111-1111-1111-111111111111"

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
		{name: "running", existing: entry{id: existingID}, wantCode: clierr.TimerRunning, wantRows: 1},
		{name: "stopped", existing: entry{id: existingID, end: end}, wantRows: 2},
		{name: "deleted_running", existing: entry{id: existingID, deletedAt: end}, wantRows: 2},
		{name: "deleted_stopped", existing: entry{id: existingID, end: end, deletedAt: end}, wantRows: 2},
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

func TestSwitchTimer_MissingContext(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st := openTestStore(t, dbPath)

	// See TestStartNewTimer_MissingContext. No entries exist, as the foreign key on
	// entries.context_id stops the context being deleted while one refers to it
	_, err := st.connectionPool.ExecContext(t.Context(), "DELETE FROM contexts WHERE id = ?", testContextID)
	if err != nil {
		t.Fatalf("delete context failed: %v", err)
	}

	started, stopped, err := st.SwitchTimer(t.Context(), time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC), "writing tests")

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
	if started.ID != "" {
		t.Errorf("started ID want empty on error, got %q", started.ID)
	}
	if stopped != nil {
		t.Errorf("stopped want nil on error, got %+v", *stopped)
	}
	if n := countRows(t, st.connectionPool, "entries"); n != 0 {
		t.Errorf("entries want 0 rows, got %d", n)
	}
}

func TestSwitchTimer_ExistingEntries(t *testing.T) {
	const insert = "INSERT INTO entries (id, context_id, start_utc, end_utc, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?)"
	const start = "2026-10-19T09:00:00.000000000Z"
	const end = "2026-10-19T10:00:00.000000000Z"
	// The running entry's ID is scanned into a uuid.UUID when it is stopped, so it must be one
	const existingID = "11111111-1111-1111-1111-111111111111"

	type entry struct {
		id             string
		end, deletedAt any
	}

	switchAt := time.Date(2026, 10, 19, 11, 0, 0, 0, time.UTC)
	const wantStoredEnd = "2026-10-19T11:00:00.000000000Z"

	tests := []struct {
		name        string
		existing    *entry // nil means an empty database
		wantStopped bool
		wantRows    int
	}{
		{name: "no_entries", wantRows: 1},
		{name: "running", existing: &entry{id: existingID}, wantStopped: true, wantRows: 2},
		{name: "stopped", existing: &entry{id: existingID, end: end}, wantRows: 2},
		{name: "deleted_running", existing: &entry{id: existingID, deletedAt: end}, wantRows: 2},
		{name: "deleted_stopped", existing: &entry{id: existingID, end: end, deletedAt: end}, wantRows: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			var existingEnd any
			if tc.existing != nil {
				_, err := st.connectionPool.ExecContext(t.Context(), insert,
					tc.existing.id, testContextID, start, tc.existing.end, start, tc.existing.deletedAt)
				if err != nil {
					t.Fatalf("existing entry %q: insert failed: %v", tc.existing.id, err)
				}
				existingEnd = tc.existing.end
			}

			before := time.Now()
			started, stopped, err := st.SwitchTimer(t.Context(), switchAt, "writing tests")
			after := time.Now()
			if err != nil {
				t.Fatalf("SwitchTimer() want nil error, got %v", err)
			}

			// Started entry
			if _, err := uuid.Parse(started.ID); err != nil {
				t.Errorf("started ID want a UUID, got %q: %v", started.ID, err)
			}
			if started.ID == existingID {
				t.Errorf("started ID want a new entry ID, got the existing %q", started.ID)
			}
			if started.Description == nil || *started.Description != "writing tests" {
				t.Errorf("started Description want %q, got %v", "writing tests", started.Description)
			}
			if !started.StartUTC.Equal(switchAt) {
				t.Errorf("started StartUTC want %v, got %v", switchAt, started.StartUTC)
			}
			if started.EndUTC != nil {
				t.Errorf("started EndUTC want nil, got %v", *started.EndUTC)
			}
			if started.UpdatedAt.Before(before) || started.UpdatedAt.After(after) {
				t.Errorf("started UpdatedAt want between %v and %v, got %v", before, after, started.UpdatedAt)
			}

			// Stopped entry
			if !tc.wantStopped {
				if stopped != nil {
					t.Errorf("stopped want nil, got %+v", *stopped)
				}
			} else {
				if stopped == nil {
					t.Fatalf("stopped want entry %q, got nil", existingID)
				}
				if stopped.ID != existingID {
					t.Errorf("stopped ID want %q, got %q", existingID, stopped.ID)
				}
				if stopped.EndUTC == nil || !stopped.EndUTC.Equal(switchAt) {
					t.Errorf("stopped EndUTC want %v, got %v", switchAt, stopped.EndUTC)
				}
				if stopped.UpdatedAt.Before(before) || stopped.UpdatedAt.After(after) {
					t.Errorf("stopped UpdatedAt want between %v and %v, got %v", before, after, stopped.UpdatedAt)
				}
				if !stopped.UpdatedAt.Equal(started.UpdatedAt) {
					t.Errorf("stopped UpdatedAt want %v (same as started), got %v", started.UpdatedAt, stopped.UpdatedAt)
				}
			}

			// Committed rows
			if n := countRows(t, st.connectionPool, "entries"); n != tc.wantRows {
				t.Errorf("entries want %d rows, got %d", tc.wantRows, n)
			}
			var startedEnd *string
			err = st.connectionPool.QueryRowContext(t.Context(), "SELECT end_utc FROM entries WHERE id = ?", started.ID).Scan(&startedEnd)
			if err != nil {
				t.Fatalf("scan started entry failed: %v", err)
			}
			if startedEnd != nil {
				t.Errorf("started end_utc want NULL, got %q", *startedEnd)
			}
			if tc.existing == nil {
				return
			}
			var storedEnd *string
			err = st.connectionPool.QueryRowContext(t.Context(), "SELECT end_utc FROM entries WHERE id = ?", existingID).Scan(&storedEnd)
			if err != nil {
				t.Fatalf("scan existing entry failed: %v", err)
			}
			wantEnd := existingEnd // entries that weren't running are left alone
			if tc.wantStopped {
				wantEnd = wantStoredEnd
			}
			if wantEnd == nil {
				if storedEnd != nil {
					t.Errorf("existing end_utc want NULL, got %q", *storedEnd)
				}
			} else if storedEnd == nil || *storedEnd != wantEnd {
				t.Errorf("existing end_utc want %q, got %v", wantEnd, storedEnd)
			}
		})
	}
}

func TestSwitchTimer_NotAfterRunningStart(t *testing.T) {
	const insert = "INSERT INTO entries (id, context_id, start_utc, updated_at) VALUES (?, ?, ?, ?)"
	const start = "2026-10-19T09:00:00.000000000Z"
	const runningID = "11111111-1111-1111-1111-111111111111"

	// The switch instant becomes the running entry's end, which must be after its start (§3.1)
	tests := []struct {
		name     string
		switchAt time.Time
	}{
		{"before_start", time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)},
		{"at_start", time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			_, err := st.connectionPool.ExecContext(t.Context(), insert, runningID, testContextID, start, start)
			if err != nil {
				t.Fatalf("running entry: insert failed: %v", err)
			}

			started, stopped, err := st.SwitchTimer(t.Context(), tc.switchAt, "writing tests")

			var errcli *clierr.Error
			if !errors.As(err, &errcli) {
				t.Fatalf("want *clierr.Error, got %T: %v", err, err)
			}
			if errcli.Code != clierr.InvalidTime {
				t.Errorf("Code want %q, got %q", clierr.InvalidTime, errcli.Code)
			}
			if started.ID != "" {
				t.Errorf("started ID want empty on error, got %q", started.ID)
			}
			if stopped != nil {
				t.Errorf("stopped want nil on error, got %+v", *stopped)
			}

			// The running entry is left running and no new entry is written
			if n := countRows(t, st.connectionPool, "entries"); n != 1 {
				t.Errorf("entries want 1 row, got %d", n)
			}
			var storedEnd *string
			err = st.connectionPool.QueryRowContext(t.Context(), "SELECT end_utc FROM entries WHERE id = ?", runningID).Scan(&storedEnd)
			if err != nil {
				t.Fatalf("scan running entry failed: %v", err)
			}
			if storedEnd != nil {
				t.Errorf("running end_utc want NULL, got %q", *storedEnd)
			}
		})
	}
}
