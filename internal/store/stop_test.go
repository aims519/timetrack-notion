package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestStopTimer_RunningEntry(t *testing.T) {
	const insert = "INSERT INTO entries (id, context_id, description, start_utc, updated_at) VALUES (?, ?, ?, ?, ?)"
	const start = "2026-10-19T09:00:00.000000000Z"
	// The running entry's ID is scanned into a uuid.UUID when it is stopped, so it must be one
	const runningID = "11111111-1111-1111-1111-111111111111"
	wantStart := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		stopUTC       time.Time
		wantStoredEnd string
	}{
		{"utc_stop", time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC), "2026-10-19T10:00:00.000000000Z"},
		{"nanoseconds_kept", time.Date(2026, 10, 19, 10, 0, 0, 123456789, time.UTC), "2026-10-19T10:00:00.123456789Z"},
		{"non_utc_stop_stored_as_utc", time.Date(2026, 10, 19, 11, 0, 0, 0, time.FixedZone("plus1", 60*60)), "2026-10-19T10:00:00.000000000Z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			_, err := st.connectionPool.ExecContext(t.Context(), insert, runningID, testContextID, "writing tests", start, start)
			if err != nil {
				t.Fatalf("running entry: insert failed: %v", err)
			}

			before := time.Now()
			got, err := st.StopTimer(t.Context(), tc.stopUTC)
			after := time.Now()
			if err != nil {
				t.Fatalf("StopTimer() want nil error, got %v", err)
			}

			// Returned entry
			if got.ID != runningID {
				t.Errorf("ID want %q, got %q", runningID, got.ID)
			}
			if got.Description == nil || *got.Description != "writing tests" {
				t.Errorf("Description want %q, got %v", "writing tests", got.Description)
			}
			if !got.StartUTC.Equal(wantStart) {
				t.Errorf("StartUTC want %v, got %v", wantStart, got.StartUTC)
			}
			if got.EndUTC == nil || !got.EndUTC.Equal(tc.stopUTC) {
				t.Errorf("EndUTC want %v, got %v", tc.stopUTC, got.EndUTC)
			}
			if got.UpdatedAt.Before(before) || got.UpdatedAt.After(after) {
				t.Errorf("UpdatedAt want between %v and %v, got %v", before, after, got.UpdatedAt)
			}

			// Committed row
			if n := countRows(t, st.connectionPool, "entries"); n != 1 {
				t.Fatalf("entries want 1 row, got %d", n)
			}
			var storedEnd *string
			err = st.connectionPool.QueryRowContext(t.Context(), "SELECT end_utc FROM entries WHERE id = ?", runningID).Scan(&storedEnd)
			if err != nil {
				t.Fatalf("scan failed: %v", err)
			}
			if storedEnd == nil || *storedEnd != tc.wantStoredEnd {
				t.Errorf("end_utc want %q, got %v", tc.wantStoredEnd, storedEnd)
			}
		})
	}
}

func TestStopTimer_NoRunningEntry(t *testing.T) {
	const insert = "INSERT INTO entries (id, context_id, start_utc, end_utc, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?)"
	const start = "2026-10-19T09:00:00.000000000Z"
	const end = "2026-10-19T10:00:00.000000000Z"
	const existingID = "11111111-1111-1111-1111-111111111111"

	type entry struct {
		id             string
		end, deletedAt any
	}

	tests := []struct {
		name     string
		existing *entry // nil means an empty database
		wantRows int
	}{
		{name: "no_entries", wantRows: 0},
		{name: "stopped", existing: &entry{id: existingID, end: end}, wantRows: 1},
		{name: "deleted_running", existing: &entry{id: existingID, deletedAt: end}, wantRows: 1},
		{name: "deleted_stopped", existing: &entry{id: existingID, end: end, deletedAt: end}, wantRows: 1},
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

			got, err := st.StopTimer(t.Context(), time.Date(2026, 10, 19, 11, 0, 0, 0, time.UTC))

			var errcli *clierr.Error
			if !errors.As(err, &errcli) {
				t.Fatalf("want *clierr.Error, got %T: %v", err, err)
			}
			if errcli.Code != clierr.NoTimerRunning {
				t.Errorf("Code want %q, got %q", clierr.NoTimerRunning, errcli.Code)
			}
			if got.ID != "" {
				t.Errorf("ID want empty on error, got %q", got.ID)
			}

			// Entries that weren't running are left alone
			if n := countRows(t, st.connectionPool, "entries"); n != tc.wantRows {
				t.Errorf("entries want %d rows, got %d", tc.wantRows, n)
			}
			if tc.existing == nil {
				return
			}
			var storedEnd *string
			err = st.connectionPool.QueryRowContext(t.Context(), "SELECT end_utc FROM entries WHERE id = ?", existingID).Scan(&storedEnd)
			if err != nil {
				t.Fatalf("scan existing entry failed: %v", err)
			}
			if existingEnd == nil {
				if storedEnd != nil {
					t.Errorf("existing end_utc want NULL, got %q", *storedEnd)
				}
			} else if storedEnd == nil || *storedEnd != existingEnd {
				t.Errorf("existing end_utc want %q, got %v", existingEnd, storedEnd)
			}
		})
	}
}

func TestStopTimer_NotAfterRunningStart(t *testing.T) {
	const insert = "INSERT INTO entries (id, context_id, start_utc, updated_at) VALUES (?, ?, ?, ?)"
	const start = "2026-10-19T09:00:00.000000000Z"
	const runningID = "11111111-1111-1111-1111-111111111111"

	// The stop instant becomes the running entry's end, which must be after its start (§3.1)
	tests := []struct {
		name    string
		stopUTC time.Time
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

			got, err := st.StopTimer(t.Context(), tc.stopUTC)

			var errcli *clierr.Error
			if !errors.As(err, &errcli) {
				t.Fatalf("want *clierr.Error, got %T: %v", err, err)
			}
			if errcli.Code != clierr.InvalidTime {
				t.Errorf("Code want %q, got %q", clierr.InvalidTime, errcli.Code)
			}
			if got.ID != "" {
				t.Errorf("ID want empty on error, got %q", got.ID)
			}

			// The running entry is left running
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
