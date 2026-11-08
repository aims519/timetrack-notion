package store

import (
	"database/sql"
	"errors"
	"maps"
	"path/filepath"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// Entry IDs are scanned into a uuid.UUID, so they must be real UUIDs. The first two share a
// 7-character prefix, so "aaaaaaa" is ambiguous while "aaaaaaa1" is unique
const (
	entryA1 = "aaaaaaa1-0000-0000-0000-000000000001"
	entryA2 = "aaaaaaa2-0000-0000-0000-000000000002"
	entryB  = "bbbbbbbb-0000-0000-0000-000000000003"
	entryC  = "cccccccc-0000-0000-0000-000000000004"
)

var attachNow = time.Date(2026, 10, 19, 18, 0, 0, 0, time.UTC)

// attachEntry is a row written straight to the entries table. Nil fields are stored as NULL
type attachEntry struct {
	id          string
	start       string
	end         any
	description any
	taskID      any
	syncState   string
	deletedAt   any
}

func insertAttachEntries(t *testing.T, st *Store, entries []attachEntry) {
	t.Helper()

	const insert = `INSERT INTO entries (id, context_id, start_utc, end_utc, description, task_page_id, sync_state, updated_at, deleted_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, e := range entries {
		syncState := e.syncState
		if syncState == "" {
			syncState = "pending"
		}
		_, err := st.connectionPool.ExecContext(t.Context(), insert,
			e.id, testContextID, e.start, e.end, e.description, e.taskID, syncState, e.start, e.deletedAt)
		if err != nil {
			t.Fatalf("entry %q: insert failed: %v", e.id, err)
		}
	}
}

// storedEntry holds the columns attach may change, so a test can check nothing else changed
type storedEntry struct {
	taskPageID sql.NullString
	syncState  string
	updatedAt  string
}

func readStoredEntries(t *testing.T, st *Store) map[string]storedEntry {
	t.Helper()

	rows, err := st.connectionPool.QueryContext(t.Context(), "SELECT id, task_page_id, sync_state, updated_at FROM entries")
	if err != nil {
		t.Fatalf("query entries failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := map[string]storedEntry{}
	for rows.Next() {
		var id string
		var e storedEntry
		if err := rows.Scan(&id, &e.taskPageID, &e.syncState, &e.updatedAt); err != nil {
			t.Fatalf("scan entry failed: %v", err)
		}
		got[id] = e
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read entries failed: %v", err)
	}

	return got
}

// checkAttachError checks err is a *clierr.Error with the wanted code, and returns its details.
// A not_found or ambiguous_selector error is about the selector, not the database, so carries
// no path (§8)
func checkAttachError(t *testing.T, err error, wantCode clierr.ErrorCode) map[string]any {
	t.Helper()

	var errcli *clierr.Error
	if !errors.As(err, &errcli) {
		t.Fatalf("want *clierr.Error, got %T: %v", err, err)
	}
	if errcli.Code != wantCode {
		t.Errorf("Code want %q, got %q: %v", wantCode, errcli.Code, err)
	}
	if _, found := errcli.Details["path"]; found {
		t.Errorf("Details want no path, got %v", errcli.Details)
	}

	return errcli.Details
}

func TestAttachTaskToEntry_ResolvesEntry(t *testing.T) {
	const (
		early  = "2026-10-19T09:00:00.000000000Z"
		middle = "2026-10-19T10:00:00.000000000Z"
		late   = "2026-10-19T11:00:00.000000000Z"
		ended  = "2026-10-19T12:00:00.000000000Z"
	)

	tests := []struct {
		name    string
		entries []attachEntry
		prefix  string
		wantID  string
	}{
		// An explicit entry wins, even over a running one
		{
			name:    "prefix_over_running",
			entries: []attachEntry{{id: entryB, start: early, end: middle}, {id: entryC, start: late}},
			prefix:  "bbbbbbb",
			wantID:  entryB,
		},
		{
			name:    "prefix_longer_than_seven",
			entries: []attachEntry{{id: entryA1, start: early, end: middle}, {id: entryA2, start: middle, end: late}},
			prefix:  "aaaaaaa1",
			wantID:  entryA1,
		},
		{
			name:    "prefix_including_dash",
			entries: []attachEntry{{id: entryB, start: early, end: middle}, {id: entryC, start: middle, end: late}},
			prefix:  "bbbbbbbb-00",
			wantID:  entryB,
		},
		{
			name:    "full_id",
			entries: []attachEntry{{id: entryB, start: early, end: middle}, {id: entryC, start: middle, end: late}},
			prefix:  entryB,
			wantID:  entryB,
		},
		// A deleted entry sharing the prefix doesn't make it ambiguous
		{
			name:    "prefix_ignores_deleted",
			entries: []attachEntry{{id: entryA1, start: early, end: middle}, {id: entryA2, start: middle, end: late, deletedAt: ended}},
			prefix:  "aaaaaaa",
			wantID:  entryA1,
		},
		// With no --entry, the running entry, even if a later-starting entry was added after it
		{
			name:    "running",
			entries: []attachEntry{{id: entryB, start: early}, {id: entryC, start: middle, end: late}},
			wantID:  entryB,
		},
		// With nothing running, the entry that started last, whatever order they were inserted in
		{
			name: "most_recent",
			entries: []attachEntry{
				{id: entryA1, start: middle, end: late},
				{id: entryC, start: late, end: ended},
				{id: entryB, start: early, end: middle},
			},
			wantID: entryC,
		},
		{
			name:    "most_recent_skips_deleted",
			entries: []attachEntry{{id: entryB, start: early, end: middle}, {id: entryC, start: middle, end: late, deletedAt: ended}},
			wantID:  entryB,
		},
		// A deleted running entry isn't running, so the fallback is the most recent live entry
		{
			name:    "deleted_running_falls_back",
			entries: []attachEntry{{id: entryB, start: early, end: middle}, {id: entryC, start: late, deletedAt: ended}},
			wantID:  entryB,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertListTasks(t, st, []listTask{{pageID: "task_open", name: "Open task"}})
			insertAttachEntries(t, st, tc.entries)
			before := readStoredEntries(t, st)

			got, err := st.AttachTaskToEntry(t.Context(), tc.prefix, nil, ptr("task_open"), false, attachNow)
			if err != nil {
				t.Fatalf("AttachTaskToEntry() want nil error, got %v", err)
			}

			if got.ID != tc.wantID {
				t.Errorf("ID want %q, got %q", tc.wantID, got.ID)
			}

			// Only the resolved entry is changed
			after := readStoredEntries(t, st)
			delete(before, tc.wantID)
			delete(after, tc.wantID)
			if !maps.Equal(after, before) {
				t.Errorf("other entries want unchanged %+v, got %+v", before, after)
			}
		})
	}
}

func TestAttachTaskToEntry_EntryErrors(t *testing.T) {
	const (
		early  = "2026-10-19T09:00:00.000000000Z"
		middle = "2026-10-19T10:00:00.000000000Z"
		late   = "2026-10-19T11:00:00.000000000Z"
	)

	tests := []struct {
		name     string
		entries  []attachEntry
		prefix   string
		wantCode clierr.ErrorCode
	}{
		// A prefix that misses fails rather than falling back to the running entry
		{
			name:     "unknown_prefix",
			entries:  []attachEntry{{id: entryB, start: early}},
			prefix:   "ddddddd",
			wantCode: clierr.NotFound,
		},
		{
			name:     "ambiguous_prefix",
			entries:  []attachEntry{{id: entryA1, start: early, end: middle}, {id: entryA2, start: middle}},
			prefix:   "aaaaaaa",
			wantCode: clierr.AmbiguousSelector,
		},
		{
			name:     "prefix_of_deleted_entry",
			entries:  []attachEntry{{id: entryB, start: early, end: middle, deletedAt: late}, {id: entryC, start: middle}},
			prefix:   "bbbbbbb",
			wantCode: clierr.NotFound,
		},
		{
			name:     "no_entries",
			wantCode: clierr.NotFound,
		},
		{
			name:     "only_deleted_entries",
			entries:  []attachEntry{{id: entryB, start: early, end: middle, deletedAt: late}, {id: entryC, start: middle, deletedAt: late}},
			wantCode: clierr.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertListTasks(t, st, []listTask{{pageID: "task_open", name: "Open task"}})
			insertAttachEntries(t, st, tc.entries)
			before := readStoredEntries(t, st)

			got, err := st.AttachTaskToEntry(t.Context(), tc.prefix, nil, ptr("task_open"), false, attachNow)

			details := checkAttachError(t, err, tc.wantCode)
			if got.ID != "" {
				t.Errorf("ID want empty on error, got %q", got.ID)
			}

			// The format of candidates isn't settled for entries yet, so only their presence is checked
			if tc.prefix != "" {
				if sel := details["selector"]; sel != tc.prefix {
					t.Errorf("Details selector want %q, got %v", tc.prefix, sel)
				}
			}
			wantCandidates := tc.wantCode == clierr.AmbiguousSelector
			if _, found := details["candidates"]; found != wantCandidates {
				t.Errorf("Details candidates present want %v, got %v", wantCandidates, found)
			}

			if after := readStoredEntries(t, st); !maps.Equal(after, before) {
				t.Errorf("entries want unchanged %+v, got %+v", before, after)
			}
		})
	}
}

func TestAttachTaskToEntry_ResolvesTask(t *testing.T) {
	tasks := []listTask{
		{pageID: "task_open", name: "Open task", order: ptr(1.0)},
		{pageID: "task_done", name: "Done task", order: ptr(2.0), done: true},
		{pageID: "task_gone", name: "Gone task", order: ptr(3.0), gone: true},
	}

	tests := []struct {
		name       string
		order      *float64
		taskID     *string
		all        bool
		wantTaskID string // empty when an error is wanted
		wantCode   clierr.ErrorCode
	}{
		{name: "id_open", taskID: ptr("task_open"), wantTaskID: "task_open"},
		{name: "order_open", order: ptr(1.0), wantTaskID: "task_open"},
		// IDs match the whole cache, done tasks included; --all only widens order numbers (§5)
		{name: "id_done", taskID: ptr("task_done"), wantTaskID: "task_done"},
		{name: "id_done_all", taskID: ptr("task_done"), all: true, wantTaskID: "task_done"},
		{name: "order_done", order: ptr(2.0), wantCode: clierr.NotFound},
		{name: "order_done_all", order: ptr(2.0), all: true, wantTaskID: "task_done"},
		// Gone tasks never match
		{name: "id_gone", taskID: ptr("task_gone"), wantCode: clierr.NotFound},
		{name: "order_gone", order: ptr(3.0), all: true, wantCode: clierr.NotFound},
		{name: "id_unknown", taskID: ptr("task_unknown"), wantCode: clierr.NotFound},
		{name: "order_unknown", order: ptr(9.0), wantCode: clierr.NotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertListTasks(t, st, tasks)
			insertAttachEntries(t, st, []attachEntry{{id: entryB, start: "2026-10-19T09:00:00.000000000Z"}})
			before := readStoredEntries(t, st)

			got, err := st.AttachTaskToEntry(t.Context(), "", tc.order, tc.taskID, tc.all, attachNow)

			if tc.wantTaskID == "" {
				checkAttachError(t, err, tc.wantCode)
				if after := readStoredEntries(t, st); !maps.Equal(after, before) {
					t.Errorf("entries want unchanged %+v, got %+v", before, after)
				}
				return
			}

			if err != nil {
				t.Fatalf("AttachTaskToEntry() want nil error, got %v", err)
			}
			if got.TaskPageID == nil || *got.TaskPageID != tc.wantTaskID {
				t.Errorf("TaskPageID want %q, got %v", tc.wantTaskID, got.TaskPageID)
			}
			if stored := readStoredEntries(t, st)[entryB].taskPageID; stored.String != tc.wantTaskID {
				t.Errorf("stored task_page_id want %q, got %+v", tc.wantTaskID, stored)
			}
		})
	}
}

func TestAttachTaskToEntry_UpdatesEntry(t *testing.T) {
	const (
		start = "2026-10-19T09:00:00.000000000Z"
		end   = "2026-10-19T10:00:00.000000000Z"
	)

	tests := []struct {
		name      string
		syncState string
		taskID    any
	}{
		// Every change makes the entry pending, so a synced row is updated next sync and a
		// blocked one is retried (§2, §6)
		{name: "pending", syncState: "pending"},
		{name: "synced", syncState: "synced"},
		{name: "failed", syncState: "failed"},
		{name: "blocked", syncState: "blocked"},
		// Attaching again replaces the task
		{name: "replaces_task", syncState: "synced", taskID: "task_old"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertListTasks(t, st, []listTask{
				{pageID: "task_old", name: "Old task"},
				{pageID: "task_new", name: "New task"},
			})
			insertAttachEntries(t, st, []attachEntry{{
				id:          entryB,
				start:       start,
				end:         end,
				description: "writing tests",
				taskID:      tc.taskID,
				syncState:   tc.syncState,
			}})

			got, err := st.AttachTaskToEntry(t.Context(), "", nil, ptr("task_new"), false, attachNow)
			if err != nil {
				t.Fatalf("AttachTaskToEntry() want nil error, got %v", err)
			}

			// The returned entry is read back after the update
			if got.TaskPageID == nil || *got.TaskPageID != "task_new" {
				t.Errorf("TaskPageID want %q, got %v", "task_new", got.TaskPageID)
			}
			if got.TaskName == nil || *got.TaskName != "New task" {
				t.Errorf("TaskName want %q, got %v", "New task", got.TaskName)
			}
			if got.SyncState != "pending" {
				t.Errorf("SyncState want %q, got %q", "pending", got.SyncState)
			}
			if !got.UpdatedAt.Equal(attachNow) {
				t.Errorf("UpdatedAt want %v, got %v", attachNow, got.UpdatedAt)
			}

			// Nothing else about the entry changes
			if got.StartUTC.Format(time.RFC3339Nano) != "2026-10-19T09:00:00Z" {
				t.Errorf("StartUTC want unchanged %s, got %v", start, got.StartUTC)
			}
			if got.EndUTC == nil || got.EndUTC.Format(time.RFC3339Nano) != "2026-10-19T10:00:00Z" {
				t.Errorf("EndUTC want unchanged %s, got %v", end, got.EndUTC)
			}
			if got.Description == nil || *got.Description != "writing tests" {
				t.Errorf("Description want unchanged %q, got %v", "writing tests", got.Description)
			}

			want := storedEntry{
				taskPageID: sql.NullString{String: "task_new", Valid: true},
				syncState:  "pending",
				updatedAt:  ToDBString(attachNow),
			}
			if stored := readStoredEntries(t, st)[entryB]; stored != want {
				t.Errorf("stored entry want %+v, got %+v", want, stored)
			}
		})
	}
}
