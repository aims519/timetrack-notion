package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/store"
)

func TestValidateStartFlags_Error(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		description *string
		task        string
		pick        bool
		at          string
		ago         string
		now         time.Time
		loc         *time.Location
		wantCode    clierr.ErrorCode
	}{
		{name: "no_description_or_task", ago: "10m", now: now, loc: time.UTC, wantCode: clierr.Usage},
		// An empty argument isn't a description, so it doesn't count as one (§3.1)
		{name: "empty_description_no_task", description: ptr(""), now: now, loc: time.UTC, wantCode: clierr.Usage},
		{name: "task_and_pick", task: "1.2", pick: true, ago: "10m", now: now, loc: time.UTC, wantCode: clierr.Usage},
		{name: "ago_and_at_given", description: ptr("organising calendar"), at: "01:00", ago: "10m", now: now, loc: time.UTC, wantCode: clierr.Usage},
		{name: "invalid_at", description: ptr("organising calendar"), at: "invalid", now: now, loc: time.UTC, wantCode: clierr.InvalidTime},
		{name: "invalid_ago", description: ptr("organising calendar"), ago: "invalid", now: now, loc: time.UTC, wantCode: clierr.InvalidTime},
		{name: "at_in_future", description: ptr("organising calendar"), at: "13:00", now: now, loc: time.UTC, wantCode: clierr.InvalidTime},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sa := startArgs{
				description:  tc.description,
				taskSelector: tc.task,
				pickTask:     tc.pick,
				at:           tc.at,
				ago:          tc.ago,
			}
			_, err := validateStartFlags(sa, tc.now, tc.loc)
			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("got %v, want clierr.Error", err)
			}

			if cliErr.Code != tc.wantCode {
				t.Errorf("got %q, want %q", cliErr.Code, tc.wantCode)
			}
		})
	}
}

func TestValidateStartFlags_Success(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		description *string
		task        string
		pick        bool
		at          string
		ago         string
		now         time.Time
		loc         *time.Location
	}{
		{name: "description_only", description: ptr("organising calendar"), now: now, loc: time.UTC},
		{name: "task_only", task: "1.1", now: now, loc: time.UTC},
		{name: "pick_only", pick: true, now: now, loc: time.UTC},
		{name: "description_at", description: ptr("organising calendar"), at: "01:00", now: now, loc: time.UTC},
		{name: "description_ago", description: ptr("organising calendar"), ago: "10m", now: now, loc: time.UTC},
		{name: "task_at", task: "1.1", at: "01:00", now: now, loc: time.UTC},
		{name: "task_ago", task: "1.1", ago: "10m", now: now, loc: time.UTC},
		{name: "pick_at", pick: true, at: "01:00", now: now, loc: time.UTC},
		{name: "pick_ago", pick: true, ago: "10m", now: now, loc: time.UTC},
		{name: "description_task_at", description: ptr("organising calendar"), task: "1.1", at: "01:00", now: now, loc: time.UTC},
		{name: "description_task_ago", description: ptr("organising calendar"), task: "1.5", ago: "10m", now: now, loc: time.UTC},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sa := startArgs{
				description:  tc.description,
				taskSelector: tc.task,
				pickTask:     tc.pick,
				at:           tc.at,
				ago:          tc.ago,
			}
			_, err := validateStartFlags(sa, tc.now, tc.loc)

			if err != nil {
				t.Fatalf("got %v, want nil", err)
			}
		})
	}
}

func TestRun_Start_UnimplementedFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantFlag string // the flag the error message should name
	}{
		{"pick_only", []string{"start", "--pick"}, "--pick"},
		{"pick_with_description", []string{"start", "--pick", "organising calendar"}, "--pick"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
			t.Setenv("TT_TIMEZONE", "UTC")
			args := append(slices.Clone(tc.args), "--output", "json")
			var stdout, stderr bytes.Buffer

			if got := Run(context.Background(), args, &stdout, &stderr); got != 2 {
				t.Errorf("Run(%v) = %d, want 2 (stderr: %q)", args, got, stderr.String())
			}

			// A failed command must write nothing to stdout
			if stdout.Len() != 0 {
				t.Errorf("Run(%v) wrote to stdout on failure: %q", args, stdout.String())
			}

			var doc struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr.Bytes(), &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr.String(), err)
			}
			if doc.Error.Code != string(clierr.Usage) {
				t.Errorf("error code want %q, got %q", clierr.Usage, doc.Error.Code)
			}
			if !strings.Contains(doc.Error.Message, tc.wantFlag) {
				t.Errorf("error message want it to name %s, got %q", tc.wantFlag, doc.Error.Message)
			}

			// Nothing was started, so a plain start on the same database succeeds
			stdout.Reset()
			stderr.Reset()
			plain := []string{"start", "organising calendar", "--output", "json"}
			if got := Run(context.Background(), plain, &stdout, &stderr); got != 0 {
				t.Errorf("Run(%v) after rejection = %d, want 0 (stderr: %q)", plain, got, stderr.String())
			}
		})
	}
}

// startOutput is the part of the start command's JSON output the switch tests check
type startOutput struct {
	Entry struct {
		ID    string  `json:"id"`
		Start string  `json:"start"`
		End   *string `json:"end"`
	} `json:"entry"`
	Stopped *struct {
		ID  string  `json:"id"`
		End *string `json:"end"`
	} `json:"stopped"`
}

// runTT runs tt with args and JSON output, returning the exit code, stdout and stderr
func runTT(t *testing.T, args ...string) (int, []byte, []byte) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	got := Run(context.Background(), append(args, "--output", "json"), &stdout, &stderr)
	return got, stdout.Bytes(), stderr.Bytes()
}

func TestRun_Start_Switch(t *testing.T) {
	const firstStart = "2026-01-01T09:00:00Z"
	const switchAt = "2026-01-01T10:00:00Z"

	tests := []struct {
		name        string
		running     bool // start a timer at firstStart before switching
		wantStopped bool
	}{
		{name: "nothing_running", wantStopped: false},
		{name: "timer_running", running: true, wantStopped: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
			t.Setenv("TT_TIMEZONE", "UTC")

			var first startOutput
			if tc.running {
				code, stdout, stderr := runTT(t, "start", "organising calendar", "--at", firstStart)
				if code != 0 {
					t.Fatalf("first start = %d, want 0 (stderr: %q)", code, stderr)
				}
				if err := json.Unmarshal(stdout, &first); err != nil {
					t.Fatalf("unable to decode first start output %q: %v", stdout, err)
				}
			}

			code, stdout, stderr := runTT(t, "start", "--switch", "writing tests", "--at", switchAt)
			if code != 0 {
				t.Fatalf("start --switch = %d, want 0 (stderr: %q)", code, stderr)
			}

			var got startOutput
			if err := json.Unmarshal(stdout, &got); err != nil {
				t.Fatalf("unable to decode start --switch output %q: %v", stdout, err)
			}

			if got.Entry.ID == "" || got.Entry.ID == first.Entry.ID {
				t.Errorf("entry.id want a new entry ID, got %q", got.Entry.ID)
			}
			if got.Entry.Start != switchAt {
				t.Errorf("entry.start want %q, got %q", switchAt, got.Entry.Start)
			}
			if got.Entry.End != nil {
				t.Errorf("entry.end want null, got %q", *got.Entry.End)
			}

			if !tc.wantStopped {
				if got.Stopped != nil {
					t.Errorf("stopped want null, got %+v", *got.Stopped)
				}
				return
			}
			if got.Stopped == nil {
				t.Fatalf("stopped want entry %q, got null", first.Entry.ID)
			}
			if got.Stopped.ID != first.Entry.ID {
				t.Errorf("stopped.id want %q, got %q", first.Entry.ID, got.Stopped.ID)
			}
			// The old entry ends at the instant the new one starts
			if got.Stopped.End == nil || *got.Stopped.End != got.Entry.Start {
				t.Errorf("stopped.end want %q, got %v", got.Entry.Start, got.Stopped.End)
			}
		})
	}
}

func TestRun_Start_SwitchBeforeRunningStart(t *testing.T) {
	t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
	t.Setenv("TT_TIMEZONE", "UTC")

	if code, _, stderr := runTT(t, "start", "organising calendar", "--at", "2026-01-01T09:00:00Z"); code != 0 {
		t.Fatalf("first start = %d, want 0 (stderr: %q)", code, stderr)
	}

	code, stdout, stderr := runTT(t, "start", "--switch", "writing tests", "--at", "2026-01-01T08:00:00Z")
	if code != 2 {
		t.Errorf("start --switch before the running start = %d, want 2 (stderr: %q)", code, stderr)
	}

	// A failed command must write nothing to stdout
	if len(stdout) != 0 {
		t.Errorf("start --switch wrote to stdout on failure: %q", stdout)
	}

	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr, &doc); err != nil {
		t.Fatalf("unable to decode error document %q: %v", stderr, err)
	}
	if doc.Error.Code != string(clierr.InvalidTime) {
		t.Errorf("error code want %q, got %q", clierr.InvalidTime, doc.Error.Code)
	}

	// The first timer is still running, so a plain start is refused
	code, _, stderr = runTT(t, "start", "organising calendar")
	if code != 4 {
		t.Errorf("start after the failed switch = %d, want 4 (stderr: %q)", code, stderr)
	}
	if err := json.Unmarshal(stderr, &doc); err != nil {
		t.Fatalf("unable to decode error document %q: %v", stderr, err)
	}
	if doc.Error.Code != string(clierr.TimerRunning) {
		t.Errorf("error code want %q, got %q", clierr.TimerRunning, doc.Error.Code)
	}
}

// startTaskOutput is the part of the start command's JSON output the --task tests check
type startTaskOutput struct {
	Entry struct {
		Description *string        `json:"description"`
		Task        *taskRefOutput `json:"task"`
	} `json:"entry"`
	Stopped *struct {
		ID string `json:"id"`
	} `json:"stopped"`
}

// Every kind of task selector attaches the task to the new entry, with or without --switch (§3.1)
func TestRun_Start_Task(t *testing.T) {
	// The only cached task, so its short ID is its last 6 characters
	want := taskRefOutput{PageID: attachTaskID, ShortID: "123456", Name: "Write tests"}

	tests := []struct {
		name            string
		running         bool // start a plain timer first
		args            []string
		wantDescription *string
		wantStopped     bool
	}{
		{name: "full_id", args: []string{"start", "--task", attachTaskID}},
		{name: "short_id", args: []string{"start", "-t", "123456"}},
		{name: "order", args: []string{"start", "-t", "1"}},
		{name: "order_with_decimal", args: []string{"start", "-t", "1.0"}},
		{name: "with_description", args: []string{"start", "-t", "1", "writing tests"}, wantDescription: ptr("writing tests")},
		{name: "switch", running: true, args: []string{"start", "--switch", "-t", "1"}, wantStopped: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			t.Setenv("TT_TIMEZONE", "UTC")
			seedAttachTask(t, dbPath)

			if tc.running {
				code, _, stderr := runTT(t, "start", "organising calendar", "--at", "2026-01-01T09:00:00Z")
				if code != 0 {
					t.Fatalf("first start = %d, want 0 (stderr: %q)", code, stderr)
				}
			}

			code, stdout, stderr := runTT(t, tc.args...)
			if code != 0 {
				t.Fatalf("Run(%v) = %d, want 0 (stderr: %q)", tc.args, code, stderr)
			}

			var got startTaskOutput
			if err := json.Unmarshal(stdout, &got); err != nil {
				t.Fatalf("unable to decode start output %q: %v", stdout, err)
			}
			if got.Entry.Task == nil || *got.Entry.Task != want {
				t.Errorf("entry.task = %+v, want %+v", got.Entry.Task, want)
			}
			if !reflect.DeepEqual(got.Entry.Description, tc.wantDescription) {
				t.Errorf("entry.description = %v, want %v", got.Entry.Description, tc.wantDescription)
			}
			if (got.Stopped != nil) != tc.wantStopped {
				t.Errorf("stopped = %+v, want stopped %t", got.Stopped, tc.wantStopped)
			}
		})
	}
}

// However a --task selector misses, start fails with not_found reporting the selector as typed,
// and starts nothing. With --switch, the running timer is left running (§3.1, §8)
func TestRun_Start_TaskNotFound_ReportsSelectorAsTyped(t *testing.T) {
	const (
		keptID = "7777777777777777777777777a111111"
		goneID = "7777777777777777777777777b222222"
	)

	tests := []struct {
		name     string
		selector string
		switched bool
	}{
		// The §8 example: parses as an order number no task has
		{name: "order_number", selector: "999"},
		// Parses as 4.1, which no task has
		{name: "order_number_as_typed", selector: "4.10"},
		// The short ID resolves in the CLI, but the task is gone, so the store finds nothing
		{name: "short_id_of_gone_task", selector: "222222"},
		// Matches no rule at all, so the CLI fails before the store is asked
		{name: "matches_no_rule", selector: "not-a-task"},
		{name: "switch_order_number", selector: "999", switched: true},
		{name: "switch_matches_no_rule", selector: "not-a-task", switched: true},
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

			args := []string{"start", "-t", tc.selector}
			if tc.switched {
				code, _, stderr := runTT(t, "start", "organising calendar", "--at", "2026-01-01T09:00:00Z")
				if code != 0 {
					t.Fatalf("first start = %d, want 0 (stderr: %q)", code, stderr)
				}
				args = append(args, "--switch")
			}

			code, stdout, stderr := runTT(t, args...)
			if code != 3 {
				t.Fatalf("Run(%v) = %d, want 3 (stdout: %q, stderr: %q)", args, code, stdout, stderr)
			}
			if len(stdout) != 0 {
				t.Errorf("stdout want empty on failure, got %q", stdout)
			}

			var got struct {
				Error struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &got); err != nil {
				t.Fatalf("unable to decode start error %q: %v", stderr, err)
			}
			if got.Error.Code != string(clierr.NotFound) {
				t.Errorf("error.code want %q, got %q", clierr.NotFound, got.Error.Code)
			}
			if want := map[string]any{"selector": tc.selector}; !reflect.DeepEqual(got.Error.Details, want) {
				t.Errorf("error.details = %v, want %v", got.Error.Details, want)
			}

			// Nothing was started: with no timer running a plain start succeeds, and with the
			// first timer still running it's refused
			wantCode := 0
			if tc.switched {
				wantCode = 4
			}
			if code, _, stderr := runTT(t, "start", "writing tests"); code != wantCode {
				t.Errorf("start after the failed start = %d, want %d (stderr: %q)", code, wantCode, stderr)
			}
		})
	}
}

// An order shared by two open tasks fails with their task items as candidates, and starts
// nothing (§3.1, §8)
func TestRun_Start_AmbiguousOrder_ReturnsTaskItems(t *testing.T) {
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

	// The selector is reported as typed, not as the number it parses to
	code, stdout, stderr := runTT(t, "start", "-t", "1.0")
	if code != 3 {
		t.Fatalf("start = %d, want 3 (stdout: %q, stderr: %q)", code, stdout, stderr)
	}
	if len(stdout) != 0 {
		t.Errorf("stdout want empty on failure, got %q", stdout)
	}

	var got attachErrorOutput
	if err := json.Unmarshal(stderr, &got); err != nil {
		t.Fatalf("unable to decode start error %q: %v", stderr, err)
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

	// Nothing was started, so a plain start succeeds
	if code, _, stderr := runTT(t, "start", "writing tests"); code != 0 {
		t.Errorf("start after the failed start = %d, want 0 (stderr: %q)", code, stderr)
	}
}

// --all adds done tasks to order numbers, so start can select one; without it the done task's
// order is not_found. IDs match done tasks either way (§3.1)
func TestRun_Start_All(t *testing.T) {
	const (
		openID = "8888888888888888888888888a111111"
		doneID = "8888888888888888888888888b222222"
	)

	tests := []struct {
		name        string
		running     bool // start a plain timer first
		args        []string
		wantTaskID  string // empty when the start should fail with not_found
		wantStopped bool
	}{
		{name: "done_order_without_all", args: []string{"start", "-t", "2"}},
		{name: "done_order_with_all", args: []string{"start", "-t", "2", "--all"}, wantTaskID: doneID},
		{name: "open_order_with_all", args: []string{"start", "-t", "1", "--all"}, wantTaskID: openID},
		{name: "done_short_id_without_all", args: []string{"start", "-t", "222222"}, wantTaskID: doneID},
		{name: "switch_done_order_without_all", running: true, args: []string{"start", "--switch", "-t", "2"}},
		{name: "switch_done_order_with_all", running: true, args: []string{"start", "--switch", "-t", "2", "--all"}, wantTaskID: doneID, wantStopped: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			t.Setenv("TT_TIMEZONE", "UTC")

			edited := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
			onStore(t, dbPath, func(st *store.Store) {
				tasks := []store.Task{
					{ContextID: store.DefaultContextID, PageID: openID, Name: "Open", SortOrder: ptr(1.0), LastEditedTime: edited},
					{ContextID: store.DefaultContextID, PageID: doneID, Name: "Done", SortOrder: ptr(2.0), Done: true, LastEditedTime: edited},
				}
				if _, err := st.RefreshTasks(t.Context(), tasks, edited, true); err != nil {
					t.Fatalf("RefreshTasks(): %v", err)
				}
			})

			if tc.running {
				code, _, stderr := runTT(t, "start", "organising calendar", "--at", "2026-01-01T09:00:00Z")
				if code != 0 {
					t.Fatalf("first start = %d, want 0 (stderr: %q)", code, stderr)
				}
			}

			code, stdout, stderr := runTT(t, tc.args...)

			if tc.wantTaskID == "" {
				if code != 3 {
					t.Fatalf("Run(%v) = %d, want 3 (stdout: %q, stderr: %q)", tc.args, code, stdout, stderr)
				}
				var got struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(stderr, &got); err != nil {
					t.Fatalf("unable to decode start error %q: %v", stderr, err)
				}
				if got.Error.Code != string(clierr.NotFound) {
					t.Errorf("error.code want %q, got %q", clierr.NotFound, got.Error.Code)
				}
				return
			}

			if code != 0 {
				t.Fatalf("Run(%v) = %d, want 0 (stderr: %q)", tc.args, code, stderr)
			}
			var got startTaskOutput
			if err := json.Unmarshal(stdout, &got); err != nil {
				t.Fatalf("unable to decode start output %q: %v", stdout, err)
			}
			if got.Entry.Task == nil || got.Entry.Task.PageID != tc.wantTaskID {
				t.Errorf("entry.task.page_id want %q, got %+v", tc.wantTaskID, got.Entry.Task)
			}
			if (got.Stopped != nil) != tc.wantStopped {
				t.Errorf("stopped = %+v, want stopped %t", got.Stopped, tc.wantStopped)
			}
		})
	}
}
