package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// The local day as UTC instants, for a zone 4 hours behind UTC, so local midnight isn't UTC midnight
var (
	testDayStart = time.Date(2026, 10, 19, 4, 0, 0, 0, time.UTC)
	testDayEnd   = time.Date(2026, 10, 20, 4, 0, 0, 0, time.UTC)
)

// statusEntry is a row to insert before reading the status. Nil fields are stored as NULL
type statusEntry struct {
	id        string
	start     string
	end       any
	taskID    any
	syncState string
	deletedAt any
}

func insertStatusEntries(t *testing.T, st *Store, entries []statusEntry) {
	t.Helper()

	const insert = `INSERT INTO entries (id, context_id, start_utc, end_utc, task_page_id, sync_state, updated_at, deleted_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	for _, e := range entries {
		syncState := e.syncState
		if syncState == "" {
			syncState = "pending"
		}
		_, err := st.connectionPool.ExecContext(t.Context(), insert,
			e.id, testContextID, e.start, e.end, e.taskID, syncState, e.start, e.deletedAt)
		if err != nil {
			t.Fatalf("entry %q: insert failed: %v", e.id, err)
		}
	}
}

func TestReadStatus_EmptyDatabase(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	got, err := st.ReadStatus(t.Context(), testDayStart, testDayEnd)
	if err != nil {
		t.Fatalf("ReadStatus() want nil error, got %v", err)
	}

	if got.Running != nil {
		t.Errorf("Running want nil, got %+v", *got.Running)
	}
	if got.Connected {
		t.Errorf("Connected want false before init, got true")
	}
	if len(got.TodayEntries) != 0 {
		t.Errorf("TodayEntries want none, got %+v", got.TodayEntries)
	}
	if got.CountSummary != (CountSummary{}) {
		t.Errorf("CountSummary want all zero, got %+v", got.CountSummary)
	}
}

func TestReadStatus_Running(t *testing.T) {
	// The running entry's ID is scanned into a uuid.UUID, so it must be one
	const runningID = "11111111-1111-1111-1111-111111111111"

	tests := []struct {
		name      string
		start     string
		wantToday bool
	}{
		{name: "started_today", start: "2026-10-19T09:00:00.000000000Z", wantToday: true},
		// A timer left running from yesterday isn't part of today (§8)
		{name: "started_yesterday", start: "2026-10-18T09:00:00.000000000Z", wantToday: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertStatusEntries(t, st, []statusEntry{{id: runningID, start: tc.start}})

			got, err := st.ReadStatus(t.Context(), testDayStart, testDayEnd)
			if err != nil {
				t.Fatalf("ReadStatus() want nil error, got %v", err)
			}

			if got.Running == nil {
				t.Fatalf("Running want entry %q, got nil", runningID)
			}
			if got.Running.ID != runningID {
				t.Errorf("Running.ID want %q, got %q", runningID, got.Running.ID)
			}
			if got.Running.EndUTC != nil {
				t.Errorf("Running.EndUTC want nil, got %v", *got.Running.EndUTC)
			}

			inToday := slices.ContainsFunc(got.TodayEntries, func(e Entry) bool { return e.ID == runningID })
			if inToday != tc.wantToday {
				t.Errorf("running entry in TodayEntries want %v, got %v", tc.wantToday, inToday)
			}
		})
	}
}

func TestReadStatus_TodayEntries(t *testing.T) {
	// Each entry is stopped an hour after it starts, so only the start decides the day
	stopped := func(id, start string) statusEntry {
		s, err := FromDBString(start)
		if err != nil {
			panic(err)
		}
		return statusEntry{id: id, start: start, end: ToDBString(s.Add(time.Hour))}
	}

	entries := []statusEntry{
		stopped("before_start", "2026-10-19T03:59:59.999999999Z"),
		stopped("at_start", "2026-10-19T04:00:00.000000000Z"),
		stopped("midday", "2026-10-19T16:00:00.000000000Z"),
		stopped("just_before_end", "2026-10-20T03:59:59.999999999Z"),
		stopped("at_end", "2026-10-20T04:00:00.000000000Z"),
		// An entry belongs to the day it started, even if it ends the next day
		stopped("across_midnight", "2026-10-20T03:30:00.000000000Z"),
	}
	deleted := stopped("deleted_today", "2026-10-19T12:00:00.000000000Z")
	deleted.deletedAt = "2026-10-19T13:00:00.000000000Z"
	entries = append(entries, deleted)

	// Chronological. across_midnight is inserted after just_before_end, so insertion order would differ
	want := []string{"at_start", "midday", "across_midnight", "just_before_end"}

	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
	insertStatusEntries(t, st, entries)

	got, err := st.ReadStatus(t.Context(), testDayStart, testDayEnd)
	if err != nil {
		t.Fatalf("ReadStatus() want nil error, got %v", err)
	}

	var gotIDs []string
	for _, e := range got.TodayEntries {
		gotIDs = append(gotIDs, e.ID)
	}
	if !slices.Equal(gotIDs, want) {
		t.Errorf("TodayEntries IDs want %v, got %v", want, gotIDs)
	}
}

func TestReadStatus_Connected(t *testing.T) {
	tests := []struct {
		name      string
		tasksDBID any
		want      bool
	}{
		{name: "local_only", tasksDBID: nil, want: false},
		{name: "connected", tasksDBID: "3f63b23e-0000-0000-0000-000000000000", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			_, err := st.connectionPool.ExecContext(t.Context(), "UPDATE contexts SET tasks_db_id = ? WHERE id = ?", tc.tasksDBID, testContextID)
			if err != nil {
				t.Fatalf("update context failed: %v", err)
			}

			got, err := st.ReadStatus(t.Context(), testDayStart, testDayEnd)
			if err != nil {
				t.Fatalf("ReadStatus() want nil error, got %v", err)
			}
			if got.Connected != tc.want {
				t.Errorf("Connected want %v, got %v", tc.want, got.Connected)
			}
		})
	}
}

func TestReadStatus_MissingContext(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	st := openTestStore(t, dbPath)

	// See TestStartNewTimer_MissingContext
	_, err := st.connectionPool.ExecContext(t.Context(), "DELETE FROM contexts WHERE id = ?", testContextID)
	if err != nil {
		t.Fatalf("delete context failed: %v", err)
	}

	_, err = st.ReadStatus(t.Context(), testDayStart, testDayEnd)

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
}

func TestReadStatus_CountSummary(t *testing.T) {
	const start = "2026-10-01T09:00:00.000000000Z"
	const end = "2026-10-01T10:00:00.000000000Z"
	const task = "3e73b23e-0000-0000-0000-000000000000"
	// The running entry's ID is scanned into a uuid.UUID, so it must be one
	const runningID = "11111111-1111-1111-1111-111111111111"

	// The counts cover every entry, not just today's, so all of these started on an earlier day
	tests := []struct {
		name    string
		entries []statusEntry
		want    CountSummary
	}{
		{"stopped_with_task", []statusEntry{{id: "a", start: start, end: end, taskID: task}}, CountSummary{Pending: 1}},
		// failed is retried next run, so it counts as pending (§2)
		{"failed", []statusEntry{{id: "a", start: start, end: end, taskID: task, syncState: "failed"}}, CountSummary{Pending: 1}},
		{"synced", []statusEntry{{id: "a", start: start, end: end, taskID: task, syncState: "synced"}}, CountSummary{}},
		{"blocked", []statusEntry{{id: "a", start: start, end: end, taskID: task, syncState: "blocked"}}, CountSummary{Blocked: 1}},
		{"stopped_no_task", []statusEntry{{id: "a", start: start, end: end}}, CountSummary{Waiting: 1}},
		{"running_with_task", []statusEntry{{id: runningID, start: start, taskID: task}}, CountSummary{Waiting: 1}},
		{"running_no_task", []statusEntry{{id: runningID, start: start}}, CountSummary{Waiting: 1}},
		// A tombstoned entry is pending until its row is archived (§2)
		{"tombstoned", []statusEntry{{id: "a", start: start, end: end, taskID: task, deletedAt: end}}, CountSummary{Pending: 1}},
		{"tombstoned_synced", []statusEntry{{id: "a", start: start, end: end, taskID: task, syncState: "synced", deletedAt: end}}, CountSummary{}},
		{"mixed", []statusEntry{
			{id: "a", start: start, end: end, taskID: task},
			{id: "b", start: start, end: end, taskID: task, syncState: "failed"},
			{id: "c", start: start, end: end, taskID: task, syncState: "synced"},
			{id: "d", start: start, end: end, taskID: task, syncState: "blocked"},
			{id: "e", start: start, end: end},
			{id: runningID, start: end},
		}, CountSummary{Pending: 2, Blocked: 1, Waiting: 2}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertStatusEntries(t, st, tc.entries)

			got, err := st.ReadStatus(t.Context(), testDayStart, testDayEnd)
			if err != nil {
				t.Fatalf("ReadStatus() want nil error, got %v", err)
			}
			if got.CountSummary != tc.want {
				t.Errorf("CountSummary want %+v, got %+v", tc.want, got.CountSummary)
			}
		})
	}
}
