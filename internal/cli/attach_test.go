package cli

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/store"
)

const attachTaskID = "5555555555555555555555555e123456"

// attachOutput is the part of the attach command's JSON output the Run tests check
type attachOutput struct {
	Entry struct {
		ID   string `json:"id"`
		Task *struct {
			PageID string `json:"page_id"`
		} `json:"task"`
	} `json:"entry"`
}

// seedAttachTask caches one open task to attach
func seedAttachTask(t *testing.T, dbPath string) {
	t.Helper()

	edited := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	onStore(t, dbPath, func(st *store.Store) {
		task := store.Task{ContextID: store.DefaultContextID, PageID: attachTaskID, Name: "Write tests", SortOrder: ptr(1.0), LastEditedTime: edited}
		if _, err := st.RefreshTasks(t.Context(), []store.Task{task}, edited, true); err != nil {
			t.Fatalf("RefreshTasks(): %v", err)
		}
	})
}

// attachErrorOutput is the attach command's JSON error, with candidates decoded loosely so
// the test sees exactly the keys each one has
type attachErrorOutput struct {
	Error struct {
		Code    string `json:"code"`
		Details struct {
			Selector   string           `json:"selector"`
			Candidates []map[string]any `json:"candidates"`
		} `json:"details"`
	} `json:"error"`
}

// An order shared by two open tasks fails with their task items as candidates (§5, §8)
func TestRun_Attach_AmbiguousOrder_ReturnsTaskItems(t *testing.T) {
	const (
		firstID  = "6666666666666666666666666a111111"
		secondID = "6666666666666666666666666b222222"
	)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("TT_DB", dbPath)
	t.Setenv("TT_TIMEZONE", "UTC")

	edited := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	onStore(t, dbPath, func(st *store.Store) {
		tasks := []store.Task{
			{ContextID: store.DefaultContextID, PageID: firstID, Name: "First", SortOrder: ptr(1.0), LastEditedTime: edited},
			{ContextID: store.DefaultContextID, PageID: secondID, Name: "Second", SortOrder: ptr(1.0), LastEditedTime: edited},
		}
		if _, err := st.RefreshTasks(t.Context(), tasks, edited, true); err != nil {
			t.Fatalf("RefreshTasks(): %v", err)
		}
	})

	code, _, stderr := runTT(t, "start", "writing tests", "--at", "2026-01-01T09:00:00Z")
	if code != 0 {
		t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
	}

	// The selector is reported as typed, not as the number it parses to
	code, stdout, stderr := runTT(t, "attach", "1.0")
	if code != 3 {
		t.Fatalf("attach = %d, want 3 (stdout: %q, stderr: %q)", code, stdout, stderr)
	}

	var got attachErrorOutput
	if err := json.Unmarshal(stderr, &got); err != nil {
		t.Fatalf("unable to decode attach error %q: %v", stderr, err)
	}
	if got.Error.Code != string(clierr.AmbiguousSelector) {
		t.Errorf("error.code want %q, got %q", clierr.AmbiguousSelector, got.Error.Code)
	}
	if got.Error.Details.Selector != "1.0" {
		t.Errorf("error.details.selector want %q, got %q", "1.0", got.Error.Details.Selector)
	}

	// Candidates are in no particular order, so sort them by page ID to compare
	candidates := got.Error.Details.Candidates
	slices.SortFunc(candidates, func(a, b map[string]any) int {
		return strings.Compare(fmt.Sprint(a["page_id"]), fmt.Sprint(b["page_id"]))
	})
	want := []map[string]any{
		{"page_id": firstID, "short_id": "111111", "order": 1.0, "name": "First", "project": nil, "done": false},
		{"page_id": secondID, "short_id": "222222", "order": 1.0, "name": "Second", "project": nil, "done": false},
	}
	if !reflect.DeepEqual(candidates, want) {
		t.Errorf("error.details.candidates = %v, want %v", candidates, want)
	}
}

// However a task selector misses, the not_found error reports it as typed, with no other
// details (§5, §8)
func TestRun_Attach_TaskNotFound_ReportsSelectorAsTyped(t *testing.T) {
	const (
		keptID = "7777777777777777777777777a111111"
		goneID = "7777777777777777777777777b222222"
	)

	tests := []struct {
		name     string
		selector string
	}{
		// Parses as 4.1, which no task has
		{name: "order_number", selector: "4.10"},
		// The short ID resolves in the CLI, but the task is gone, so the store finds nothing
		{name: "short_id_of_gone_task", selector: "222222"},
		// Matches no rule at all, so the CLI fails before the store is asked
		{name: "matches_no_rule", selector: "not-a-task"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			t.Setenv("TT_TIMEZONE", "UTC")

			// Two full refreshes, the second without goneID, so it's cached but marked gone
			edited := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
			onStore(t, dbPath, func(st *store.Store) {
				task := func(id string) store.Task {
					return store.Task{ContextID: store.DefaultContextID, PageID: id, Name: id, SortOrder: ptr(1.0), LastEditedTime: edited}
				}
				if _, err := st.RefreshTasks(t.Context(), []store.Task{task(keptID), task(goneID)}, edited, true); err != nil {
					t.Fatalf("first RefreshTasks(): %v", err)
				}
				if _, err := st.RefreshTasks(t.Context(), []store.Task{task(keptID)}, edited.Add(time.Hour), true); err != nil {
					t.Fatalf("second RefreshTasks(): %v", err)
				}
			})

			code, _, stderr := runTT(t, "start", "writing tests", "--at", "2026-01-01T09:00:00Z")
			if code != 0 {
				t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
			}

			code, stdout, stderr := runTT(t, "attach", tc.selector)
			if code != 3 {
				t.Fatalf("attach = %d, want 3 (stdout: %q, stderr: %q)", code, stdout, stderr)
			}

			var got struct {
				Error struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &got); err != nil {
				t.Fatalf("unable to decode attach error %q: %v", stderr, err)
			}
			if got.Error.Code != string(clierr.NotFound) {
				t.Errorf("error.code want %q, got %q", clierr.NotFound, got.Error.Code)
			}
			if want := map[string]any{"selector": tc.selector}; !reflect.DeepEqual(got.Error.Details, want) {
				t.Errorf("error.details = %v, want %v", got.Error.Details, want)
			}
		})
	}
}

// Entry IDs are stored in lower case, so --entry matches them whatever case it's typed in
func TestRun_Attach_EntryIgnoresCase(t *testing.T) {
	tests := []struct {
		name   string
		prefix func(id string) string
	}{
		{name: "lower_case_prefix", prefix: func(id string) string { return id[:7] }},
		{name: "upper_case_prefix", prefix: func(id string) string { return strings.ToUpper(id[:7]) }},
		{name: "mixed_case_prefix", prefix: func(id string) string { return strings.ToUpper(id[:4]) + id[4:8] }},
		{name: "upper_case_full_id", prefix: strings.ToUpper},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			t.Setenv("TT_TIMEZONE", "UTC")
			seedAttachTask(t, dbPath)

			// A stopped entry to name with --entry, then a running one the fallback would choose instead
			code, stdout, stderr := runTT(t, "start", "first", "--at", "2026-01-01T09:00:00Z")
			if code != 0 {
				t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
			}
			var first startOutput
			if err := json.Unmarshal(stdout, &first); err != nil {
				t.Fatalf("unable to decode start output %q: %v", stdout, err)
			}
			code, _, stderr = runTT(t, "start", "--switch", "second", "--at", "2026-01-01T10:00:00Z")
			if code != 0 {
				t.Fatalf("start --switch = %d, want 0 (stderr: %q)", code, stderr)
			}

			code, stdout, stderr = runTT(t, "attach", attachTaskID, "--entry", tc.prefix(first.Entry.ID))
			if code != 0 {
				t.Fatalf("attach = %d, want 0 (stderr: %q)", code, stderr)
			}

			var got attachOutput
			if err := json.Unmarshal(stdout, &got); err != nil {
				t.Fatalf("unable to decode attach output %q: %v", stdout, err)
			}
			if got.Entry.ID != first.Entry.ID {
				t.Errorf("entry.id want %q, got %q", first.Entry.ID, got.Entry.ID)
			}
			if got.Entry.Task == nil || got.Entry.Task.PageID != attachTaskID {
				t.Errorf("entry.task.page_id want %q, got %+v", attachTaskID, got.Entry.Task)
			}
		})
	}
}

// entryKeys are the keys of an entry object (§8). Candidates for an entry prefix are entries,
// so each must have exactly these
var entryKeys = []string{"deleted", "description", "end", "id", "minutes", "seconds", "start", "sync_error", "sync_state", "task"}

// decodeAmbiguousEntryDetails marshals the error's details as the JSON output would, and
// decodes them loosely so the test sees exactly the keys each candidate has
func decodeAmbiguousEntryDetails(t *testing.T, errcli *clierr.Error) (selector string, candidates []map[string]any) {
	t.Helper()

	data, err := json.Marshal(errcli.Details)
	if err != nil {
		t.Fatalf("unable to marshal details %v: %v", errcli.Details, err)
	}
	var details struct {
		Selector   string           `json:"selector"`
		Candidates []map[string]any `json:"candidates"`
	}
	if err := json.Unmarshal(data, &details); err != nil {
		t.Fatalf("unable to decode details %s: %v", data, err)
	}

	return details.Selector, details.Candidates
}

func TestAmbiguousEntryError(t *testing.T) {
	const (
		idA1 = "aaaaaaa1-0000-0000-0000-000000000001"
		idA2 = "aaaaaaa2-0000-0000-0000-000000000002"
		idA3 = "aaaaaaa3-0000-0000-0000-000000000003"
	)
	start := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	entry := func(id string) store.Entry {
		return store.Entry{ID: id, StartUTC: start, SyncState: "pending", UpdatedAt: start}
	}

	tests := []struct {
		name       string
		selector   string
		candidates []store.Entry
	}{
		{name: "two_candidates", selector: "aaaaaaa", candidates: []store.Entry{entry(idA1), entry(idA2)}},
		// The selector is reported as typed, even though it's matched ignoring case (§3)
		{name: "selector_kept_as_typed", selector: "AAAAAAA", candidates: []store.Entry{entry(idA1), entry(idA2)}},
		{name: "three_candidates", selector: "aaaaaaa", candidates: []store.Entry{entry(idA3), entry(idA1), entry(idA2)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ambiguousEntryError(tc.selector, tc.candidates, map[string]string{})

			if got.Code != clierr.AmbiguousSelector {
				t.Errorf("Code want %q, got %q", clierr.AmbiguousSelector, got.Code)
			}

			selector, candidates := decodeAmbiguousEntryDetails(t, got)
			if selector != tc.selector {
				t.Errorf("selector want %q, got %q", tc.selector, selector)
			}

			// Every candidate is listed, in the order the store gave them
			if len(candidates) != len(tc.candidates) {
				t.Fatalf("got %d candidates, want %d: %v", len(candidates), len(tc.candidates), candidates)
			}
			for i, c := range candidates {
				if c["id"] != tc.candidates[i].ID {
					t.Errorf("candidate %d id want %q, got %v", i, tc.candidates[i].ID, c["id"])
				}
				if keys := slices.Sorted(maps.Keys(c)); !slices.Equal(keys, entryKeys) {
					t.Errorf("candidate %d keys = %q, want %q", i, keys, entryKeys)
				}
			}
		})
	}
}

// Candidates are mapped as attach maps its own entry, so a stopped entry has its duration and
// an attached task has its short ID
func TestAmbiguousEntryError_CandidateValues(t *testing.T) {
	start := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	end := start.Add(72 * time.Minute)

	stopped := store.Entry{
		ID:          "aaaaaaa1-0000-0000-0000-000000000001",
		Description: ptr("writing tests"),
		StartUTC:    start,
		EndUTC:      &end,
		TaskPageID:  ptr("3f63b23e837922"),
		TaskName:    ptr("Write the sync engine"),
		SyncState:   "synced",
		UpdatedAt:   end,
	}
	running := store.Entry{
		ID:        "aaaaaaa2-0000-0000-0000-000000000002",
		StartUTC:  end,
		SyncState: "pending",
		UpdatedAt: end,
	}

	got := ambiguousEntryError("aaaaaaa", []store.Entry{stopped, running}, map[string]string{"3f63b23e837922": "837922"})

	_, candidates := decodeAmbiguousEntryDetails(t, got)
	if len(candidates) != 2 {
		t.Fatalf("got %d candidates, want 2: %v", len(candidates), candidates)
	}

	wantStopped := map[string]any{
		"id":          stopped.ID,
		"start":       "2026-10-19T09:00:00Z",
		"end":         "2026-10-19T10:12:00Z",
		"seconds":     4320.0,
		"minutes":     72.0,
		"description": "writing tests",
		"task":        map[string]any{"page_id": "3f63b23e837922", "short_id": "837922", "name": "Write the sync engine"},
		"sync_state":  "synced",
		"sync_error":  nil,
		"deleted":     false,
	}
	if !reflect.DeepEqual(candidates[0], wantStopped) {
		t.Errorf("stopped candidate = %v, want %v", candidates[0], wantStopped)
	}

	// A running candidate has no end, duration or task
	wantRunning := map[string]any{
		"id":          running.ID,
		"start":       "2026-10-19T10:12:00Z",
		"end":         nil,
		"seconds":     nil,
		"minutes":     nil,
		"description": nil,
		"task":        nil,
		"sync_state":  "pending",
		"sync_error":  nil,
		"deleted":     false,
	}
	if !reflect.DeepEqual(candidates[1], wantRunning) {
		t.Errorf("running candidate = %v, want %v", candidates[1], wantRunning)
	}
}

// taskRefOutput is an entry's task as the JSON output gives it
type taskRefOutput struct {
	PageID  string `json:"page_id"`
	ShortID string `json:"short_id"`
	Name    string `json:"name"`
}

// Once a task is attached, every command that outputs the entry gives the task its short ID,
// never an empty one (§8)
func TestRun_AttachedEntry_TaskHasShortID(t *testing.T) {
	// The only cached task, so its short ID is its last 6 characters
	want := taskRefOutput{PageID: attachTaskID, ShortID: "123456", Name: "Write tests"}

	tests := []struct {
		name string
		args []string
		// task extracts the attached entry's task from the command's output
		task func(t *testing.T, stdout []byte) *taskRefOutput
	}{
		{
			name: "status_running",
			args: []string{"status"},
			task: func(t *testing.T, stdout []byte) *taskRefOutput {
				var got struct {
					Running *struct {
						Task *taskRefOutput `json:"task"`
					} `json:"running"`
				}
				if err := json.Unmarshal(stdout, &got); err != nil {
					t.Fatalf("unable to decode status output %q: %v", stdout, err)
				}
				if got.Running == nil {
					t.Fatalf("running want the attached entry, got null")
				}
				return got.Running.Task
			},
		},
		{
			name: "stop",
			args: []string{"stop", "--at", "2026-01-01T10:00:00Z"},
			task: func(t *testing.T, stdout []byte) *taskRefOutput {
				var got struct {
					Entry struct {
						Task *taskRefOutput `json:"task"`
					} `json:"entry"`
				}
				if err := json.Unmarshal(stdout, &got); err != nil {
					t.Fatalf("unable to decode stop output %q: %v", stdout, err)
				}
				return got.Entry.Task
			},
		},
		{
			name: "start_switch_stopped",
			args: []string{"start", "--switch", "next thing", "--at", "2026-01-01T10:00:00Z"},
			task: func(t *testing.T, stdout []byte) *taskRefOutput {
				var got struct {
					Stopped *struct {
						Task *taskRefOutput `json:"task"`
					} `json:"stopped"`
				}
				if err := json.Unmarshal(stdout, &got); err != nil {
					t.Fatalf("unable to decode start output %q: %v", stdout, err)
				}
				if got.Stopped == nil {
					t.Fatalf("stopped want the attached entry, got null")
				}
				return got.Stopped.Task
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			t.Setenv("TT_TIMEZONE", "UTC")
			seedAttachTask(t, dbPath)

			code, _, stderr := runTT(t, "start", "writing tests", "--at", "2026-01-01T09:00:00Z")
			if code != 0 {
				t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
			}
			code, _, stderr = runTT(t, "attach", attachTaskID)
			if code != 0 {
				t.Fatalf("attach = %d, want 0 (stderr: %q)", code, stderr)
			}

			code, stdout, stderr := runTT(t, tc.args...)
			if code != 0 {
				t.Fatalf("%s = %d, want 0 (stderr: %q)", tc.args[0], code, stderr)
			}

			got := tc.task(t, stdout)
			if got == nil || *got != want {
				t.Errorf("task = %+v, want %+v", got, want)
			}
		})
	}
}

// With no selector, attach opens the picker only if prompts are allowed. In tests stdout and
// stderr are buffers, never terminals, so both cases fail as usage errors and leave the entry
// unattached (§3, §8)
func TestRun_Attach_NoSelectorWithoutPrompts_IsUsageError(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "no_input_flag", args: []string{"attach", "--no-input"}},
		{name: "no_terminal", args: []string{"attach"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			t.Setenv("TT_TIMEZONE", "UTC")
			seedAttachTask(t, dbPath)

			code, _, stderr := runTT(t, "start", "writing tests", "--at", "2026-01-01T09:00:00Z")
			if code != 0 {
				t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
			}

			code, stdout, stderr := runTT(t, tc.args...)
			if code != 2 {
				t.Fatalf("attach = %d, want 2 (stdout: %q, stderr: %q)", code, stdout, stderr)
			}
			if len(stdout) != 0 {
				t.Errorf("stdout want empty, got %q", stdout)
			}

			var got attachErrorOutput
			if err := json.Unmarshal(stderr, &got); err != nil {
				t.Fatalf("unable to decode attach error %q: %v", stderr, err)
			}
			if got.Error.Code != string(clierr.Usage) {
				t.Errorf("error.code want %q, got %q", clierr.Usage, got.Error.Code)
			}

			// The running entry still has no task
			code, stdout, stderr = runTT(t, "status")
			if code != 0 {
				t.Fatalf("status = %d, want 0 (stderr: %q)", code, stderr)
			}
			var status struct {
				Running *struct {
					Task *taskRefOutput `json:"task"`
				} `json:"running"`
			}
			if err := json.Unmarshal(stdout, &status); err != nil {
				t.Fatalf("unable to decode status output %q: %v", stdout, err)
			}
			if status.Running == nil {
				t.Fatalf("running want the started entry, got null")
			}
			if status.Running.Task != nil {
				t.Errorf("running.task want null, got %+v", status.Running.Task)
			}
		})
	}
}
