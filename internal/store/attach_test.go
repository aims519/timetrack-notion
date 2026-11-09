package store

import (
	"database/sql"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// Entry IDs are scanned into a uuid.UUID, so they must be real UUIDs. The A entries share a
// 7-character prefix, so "aaaaaaa" is ambiguous while "aaaaaaa1" is unique
const (
	entryA1 = "aaaaaaa1-0000-0000-0000-000000000001"
	entryA2 = "aaaaaaa2-0000-0000-0000-000000000002"
	entryA3 = "aaaaaaa3-0000-0000-0000-000000000005"
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

// checkTaskNotFound checks err is ErrTaskNotFound. The store reports only that no task matched;
// the CLI builds the not_found error with the selector as typed (§8)
func checkTaskNotFound(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("want ErrTaskNotFound, got %T: %v", err, err)
	}
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

			got, err := st.AttachTaskToEntry(t.Context(), tc.prefix, TaskSelector{PageID: ptr("task_open"), All: false}, attachNow)
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

			got, err := st.AttachTaskToEntry(t.Context(), tc.prefix, TaskSelector{PageID: ptr("task_open"), All: false}, attachNow)

			details := checkAttachError(t, err, tc.wantCode)
			if got.ID != "" {
				t.Errorf("ID want empty on error, got %q", got.ID)
			}

			if tc.prefix != "" {
				if sel := details["selector"]; sel != tc.prefix {
					t.Errorf("Details selector want %q, got %v", tc.prefix, sel)
				}
			}
			if _, found := details["candidates"]; found {
				t.Errorf("Details want no candidates for not_found, got %v", details)
			}

			if after := readStoredEntries(t, st); !maps.Equal(after, before) {
				t.Errorf("entries want unchanged %+v, got %+v", before, after)
			}
		})
	}
}

// An ambiguous entry prefix returns every live matching entry, read in full, so the CLI can list
// them as entries (§3, §8, decision 27)
func TestAttachTaskToEntry_AmbiguousEntryPrefix(t *testing.T) {
	const (
		early  = "2026-10-19T09:00:00.000000000Z"
		middle = "2026-10-19T10:00:00.000000000Z"
		late   = "2026-10-19T11:00:00.000000000Z"
		ended  = "2026-10-19T12:00:00.000000000Z"
	)

	tests := []struct {
		name           string
		entries        []attachEntry
		prefix         string
		wantCandidates []string // sorted
	}{
		{
			name: "two_matches",
			entries: []attachEntry{
				{id: entryA1, start: early, end: middle, description: "first"},
				{id: entryA2, start: middle, description: "second"},
				{id: entryB, start: late, end: ended},
			},
			prefix:         "aaaaaaa",
			wantCandidates: []string{entryA1, entryA2},
		},
		// Every match is listed, not just enough to show it's ambiguous
		{
			name: "three_matches",
			entries: []attachEntry{
				{id: entryA1, start: early, end: middle, description: "first"},
				{id: entryA2, start: middle, end: late, description: "second"},
				{id: entryA3, start: late, description: "third"},
			},
			prefix:         "aaaaaaa",
			wantCandidates: []string{entryA1, entryA2, entryA3},
		},
		// A deleted entry sharing the prefix isn't a candidate
		{
			name: "deleted_not_listed",
			entries: []attachEntry{
				{id: entryA1, start: early, end: middle, description: "first"},
				{id: entryA2, start: middle, end: late, description: "second"},
				{id: entryA3, start: late, end: ended, description: "third", deletedAt: ended},
			},
			prefix:         "aaaaaaa",
			wantCandidates: []string{entryA1, entryA2},
		},
	}

	wantDescriptions := map[string]string{entryA1: "first", entryA2: "second", entryA3: "third"}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertListTasks(t, st, []listTask{{pageID: "task_open", name: "Open task"}})
			insertAttachEntries(t, st, tc.entries)
			before := readStoredEntries(t, st)

			_, err := st.AttachTaskToEntry(t.Context(), tc.prefix, TaskSelector{PageID: ptr("task_open")}, attachNow)

			// The store returns the matching entries; the CLI turns them into entry documents
			var ambErr *AmbiguousEntryError
			if !errors.As(err, &ambErr) {
				t.Fatalf("want *AmbiguousEntryError, got %T: %v", err, err)
			}

			gotIDs := make([]string, len(ambErr.Candidates))
			for i, e := range ambErr.Candidates {
				gotIDs[i] = e.ID
			}
			slices.Sort(gotIDs)
			if !slices.Equal(gotIDs, tc.wantCandidates) {
				t.Fatalf("Candidates = %q, want %q", gotIDs, tc.wantCandidates)
			}

			// Candidates are whole entries, not just IDs, so they can be shown as entries
			for _, e := range ambErr.Candidates {
				want := wantDescriptions[e.ID]
				if e.Description == nil || *e.Description != want {
					t.Errorf("candidate %s Description want %q, got %v", e.ID, want, e.Description)
				}
				if e.StartUTC.IsZero() {
					t.Errorf("candidate %s StartUTC want set, got zero", e.ID)
				}
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
		wantTaskID string // empty when no task should be found
	}{
		{name: "id_open", taskID: ptr("task_open"), wantTaskID: "task_open"},
		{name: "order_open", order: ptr(1.0), wantTaskID: "task_open"},
		// IDs match the whole cache, done tasks included; --all only widens order numbers (§5)
		{name: "id_done", taskID: ptr("task_done"), wantTaskID: "task_done"},
		{name: "id_done_all", taskID: ptr("task_done"), all: true, wantTaskID: "task_done"},
		{name: "order_done", order: ptr(2.0)},
		{name: "order_done_all", order: ptr(2.0), all: true, wantTaskID: "task_done"},
		// Gone tasks never match
		{name: "id_gone", taskID: ptr("task_gone")},
		{name: "order_gone", order: ptr(3.0), all: true},
		{name: "id_unknown", taskID: ptr("task_unknown")},
		{name: "order_unknown", order: ptr(9.0)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertListTasks(t, st, tasks)
			insertAttachEntries(t, st, []attachEntry{{id: entryB, start: "2026-10-19T09:00:00.000000000Z"}})
			before := readStoredEntries(t, st)

			got, err := st.AttachTaskToEntry(t.Context(), "", TaskSelector{Order: tc.order, PageID: tc.taskID, All: tc.all}, attachNow)

			if tc.wantTaskID == "" {
				checkTaskNotFound(t, err)
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

			got, err := st.AttachTaskToEntry(t.Context(), "", TaskSelector{PageID: ptr("task_new")}, attachNow)
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

func TestAttachTaskToEntry_OrderNumbers(t *testing.T) {
	// Order numbers cover only the current project's open tasks, and match the whole cache
	// when there is no default project, as in task list (§5)
	tasks := []listTask{
		{pageID: "p1_one", name: "P1 one", project: ptr("project1"), order: ptr(1.0)},
		{pageID: "p2_one", name: "P2 one", project: ptr("project2"), order: ptr(1.0)},
		{pageID: "none_two", name: "No project two", order: ptr(2.0)},
		{pageID: "p1_three", name: "P1 three", project: ptr("project1"), order: ptr(3.0)},
		{pageID: "p1_three_dup", name: "P1 three again", project: ptr("project1"), order: ptr(3.0)},
		{pageID: "p1_four", name: "P1 four", project: ptr("project1"), order: ptr(4.0)},
		{pageID: "p1_four_done", name: "P1 four done", project: ptr("project1"), order: ptr(4.0), done: true},
		{pageID: "p1_five", name: "P1 five", project: ptr("project1"), order: ptr(5.0)},
		{pageID: "p1_five_gone", name: "P1 five gone", project: ptr("project1"), order: ptr(5.0), gone: true},
		{pageID: "p2_six", name: "P2 six", project: ptr("project2"), order: ptr(6.0)},
	}

	tests := []struct {
		name           string
		defaultProject *string
		order          *float64
		taskID         *string
		all            bool
		wantTaskID     string   // empty when no single task should be found
		wantCandidates []string // page IDs, in any order, when the order is ambiguous; nil when not found
	}{
		// Another project's task with the same order doesn't count
		{name: "same_order_other_project", defaultProject: ptr("project1"), order: ptr(1.0), wantTaskID: "p1_one"},
		{name: "only_in_other_project", defaultProject: ptr("project1"), order: ptr(6.0)},
		{name: "task_without_project", defaultProject: ptr("project1"), order: ptr(2.0)},
		// With no default project, every project counts
		{name: "no_default_unique", order: ptr(6.0), wantTaskID: "p2_six"},
		{name: "no_default_task_without_project", order: ptr(2.0), wantTaskID: "none_two"},
		{name: "no_default_across_projects", order: ptr(1.0), wantCandidates: []string{"p1_one", "p2_one"}},
		{name: "duplicate_in_project", defaultProject: ptr("project1"), order: ptr(3.0), wantCandidates: []string{"p1_three", "p1_three_dup"}},
		// Done and gone tasks don't make an order ambiguous, unless --all adds the done ones
		{name: "duplicate_done", defaultProject: ptr("project1"), order: ptr(4.0), wantTaskID: "p1_four"},
		{name: "duplicate_done_all", defaultProject: ptr("project1"), order: ptr(4.0), all: true, wantCandidates: []string{"p1_four", "p1_four_done"}},
		{name: "duplicate_gone", defaultProject: ptr("project1"), order: ptr(5.0), all: true, wantTaskID: "p1_five"},
		// IDs match the whole cache, whatever the current project
		{name: "id_other_project", defaultProject: ptr("project1"), taskID: ptr("p2_six"), wantTaskID: "p2_six"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertListTasks(t, st, tasks)
			setDefaultProject(t, st, tc.defaultProject)
			insertAttachEntries(t, st, []attachEntry{{id: entryB, start: "2026-10-19T09:00:00.000000000Z"}})
			before := readStoredEntries(t, st)

			got, err := st.AttachTaskToEntry(t.Context(), "", TaskSelector{Order: tc.order, PageID: tc.taskID, All: tc.all}, attachNow)

			if tc.wantTaskID == "" {
				if tc.wantCandidates != nil {
					// The store returns the matching tasks; the CLI turns them into task items (§8)
					var ambErr *AmbiguousTaskError
					if !errors.As(err, &ambErr) {
						t.Fatalf("want *AmbiguousTaskError, got %T: %v", err, err)
					}
					gotIDs := listedPageIDs(ambErr.Candidates)
					slices.Sort(gotIDs)
					if !slices.Equal(gotIDs, tc.wantCandidates) {
						t.Errorf("Candidates = %q, want %q", gotIDs, tc.wantCandidates)
					}
				} else {
					checkTaskNotFound(t, err)
				}

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

// The candidates' projects come from the task's own context and leave out gone projects, as in
// task list. A project cached under another context must not duplicate a task, which would
// also make a unique order number look ambiguous
func TestAttachTaskToEntry_OrderNumbers_CandidateProjects(t *testing.T) {
	tasks := []listTask{
		{pageID: "p1_a", name: "P1 a", project: ptr("project1"), order: ptr(1.0)},
		{pageID: "p1_b", name: "P1 b", project: ptr("project1"), order: ptr(1.0)},
		{pageID: "p2_a", name: "P2 a", project: ptr("project2"), order: ptr(2.0)},
		{pageID: "p2_b", name: "P2 b", project: ptr("project2"), order: ptr(2.0)},
		{pageID: "p1_c", name: "P1 c", project: ptr("project1"), order: ptr(3.0)},
	}
	projects := []listProject{
		{pageID: "project1", name: "Sabbatical"},
		{contextID: otherContextID, pageID: "project1", name: "Other context's name"},
		{pageID: "project2", name: "Old", gone: true},
	}

	type candidate struct {
		pageID      string
		projectID   string
		projectName *string
	}

	tests := []struct {
		name           string
		order          float64
		wantTaskID     string      // empty when the order is ambiguous
		wantCandidates []candidate // sorted by page ID
	}{
		{
			name:  "project_in_two_contexts",
			order: 1.0,
			wantCandidates: []candidate{
				{pageID: "p1_a", projectID: "project1", projectName: ptr("Sabbatical")},
				{pageID: "p1_b", projectID: "project1", projectName: ptr("Sabbatical")},
			},
		},
		{
			// The task keeps its project ID; only the name is unknown
			name:  "gone_project",
			order: 2.0,
			wantCandidates: []candidate{
				{pageID: "p2_a", projectID: "project2"},
				{pageID: "p2_b", projectID: "project2"},
			},
		},
		{name: "unique_order_in_project_in_two_contexts", order: 3.0, wantTaskID: "p1_c"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertOtherContext(t, st)
			insertListProjects(t, st, projects)
			insertListTasks(t, st, tasks)
			insertAttachEntries(t, st, []attachEntry{{id: entryB, start: "2026-10-19T09:00:00.000000000Z"}})

			got, err := st.AttachTaskToEntry(t.Context(), "", TaskSelector{Order: ptr(tc.order)}, attachNow)

			if tc.wantTaskID != "" {
				if err != nil {
					t.Fatalf("AttachTaskToEntry() want nil error, got %v", err)
				}
				if got.TaskPageID == nil || *got.TaskPageID != tc.wantTaskID {
					t.Errorf("TaskPageID want %q, got %v", tc.wantTaskID, got.TaskPageID)
				}
				return
			}

			var ambErr *AmbiguousTaskError
			if !errors.As(err, &ambErr) {
				t.Fatalf("want *AmbiguousTaskError, got %T: %v", err, err)
			}

			gotCandidates := make([]candidate, len(ambErr.Candidates))
			for i, task := range ambErr.Candidates {
				c := candidate{pageID: task.PageID, projectName: task.ProjectName}
				if task.ProjectPageID != nil {
					c.projectID = *task.ProjectPageID
				}
				gotCandidates[i] = c
			}
			slices.SortFunc(gotCandidates, func(a, b candidate) int { return strings.Compare(a.pageID, b.pageID) })

			if len(gotCandidates) != len(tc.wantCandidates) {
				described := make([]string, len(gotCandidates))
				for i, c := range gotCandidates {
					name := "<nil>"
					if c.projectName != nil {
						name = *c.projectName
					}
					described[i] = c.pageID + " in " + name
				}
				t.Fatalf("got %d candidates %q, want %d", len(gotCandidates), described, len(tc.wantCandidates))
			}
			for i, want := range tc.wantCandidates {
				g := gotCandidates[i]
				if g.pageID != want.pageID || g.projectID != want.projectID {
					t.Errorf("candidate %d = %s in %q, want %s in %q", i, g.pageID, g.projectID, want.pageID, want.projectID)
				}
				checkStringPtr(t, want.pageID+" project name", g.projectName, want.projectName)
			}
		})
	}
}
