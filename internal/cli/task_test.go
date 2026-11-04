package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/store"
)

// These cases all fail before any Notion request is made, so they need no fake server.
// The token is checked first, so a missing token is reported even before init (DESIGN.md §9)
func TestRun_TaskRefresh_Errors(t *testing.T) {
	tests := []struct {
		name     string
		token    *string // nil leaves NOTION_TOKEN unset
		args     []string
		wantExit int
		wantCode clierr.ErrorCode
	}{
		{name: "token_unset", token: nil, args: []string{"task", "refresh"}, wantExit: 5, wantCode: clierr.NoToken},
		{name: "token_empty", token: ptr(""), args: []string{"task", "refresh"}, wantExit: 5, wantCode: clierr.NoToken},
		{name: "token_unset_full", token: nil, args: []string{"task", "refresh", "--full"}, wantExit: 5, wantCode: clierr.NoToken},
		{name: "not_initialised", token: ptr("test-token"), args: []string{"task", "refresh"}, wantExit: 5, wantCode: clierr.NotInitialised},
		{name: "not_initialised_full", token: ptr("test-token"), args: []string{"task", "refresh", "--full"}, wantExit: 5, wantCode: clierr.NotInitialised},
		{name: "extra_argument", token: ptr("test-token"), args: []string{"task", "refresh", "everything"}, wantExit: 2, wantCode: clierr.Usage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			setNotionToken(t, tc.token)

			code, stdout, stderr := runTT(t, tc.args...)
			if code != tc.wantExit {
				t.Errorf("Run(%v) = %d, want %d (stderr: %q)", tc.args, code, tc.wantExit, stderr)
			}

			// A failed command must write nothing to stdout
			if len(stdout) != 0 {
				t.Errorf("Run(%v) wrote to stdout on failure: %q", tc.args, stdout)
			}

			var doc struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr, err)
			}
			if doc.Error.Code != string(tc.wantCode) {
				t.Errorf("error code want %q, got %q", tc.wantCode, doc.Error.Code)
			}

			// Nothing reached Notion, so this isn't a failed refresh and mustn't start the back-off
			checkCacheStateUntouched(t, dbPath)
		})
	}
}

// setNotionToken sets NOTION_TOKEN to token for the test, or unsets it if token is nil.
// Either way the developer's own value is restored afterwards
func setNotionToken(t *testing.T, token *string) {
	t.Helper()

	t.Setenv("NOTION_TOKEN", "")
	if token != nil {
		t.Setenv("NOTION_TOKEN", *token)
		return
	}

	if err := os.Unsetenv("NOTION_TOKEN"); err != nil {
		t.Fatalf("error unsetting NOTION_TOKEN: %v", err)
	}
}

// checkCacheStateUntouched fails the test if any task or project refresh time has been recorded
func checkCacheStateUntouched(t *testing.T, dbPath string) {
	t.Helper()

	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()

	got, err := st.CacheState(context.Background())
	if err != nil {
		t.Fatalf("CacheState(): %v", err)
	}

	if got.TasksRefreshedAt != nil {
		t.Errorf("tasks_refreshed_at = %v, want null", *got.TasksRefreshedAt)
	}
	if got.TasksRefreshFailedAt != nil {
		t.Errorf("tasks_refresh_failed_at = %v, want null", *got.TasksRefreshFailedAt)
	}
	if got.TasksFullRefreshedAt != nil {
		t.Errorf("tasks_full_refreshed_at = %v, want null", *got.TasksFullRefreshedAt)
	}
	if got.ProjectsRefreshedAt != nil {
		t.Errorf("projects_refreshed_at = %v, want null", *got.ProjectsRefreshedAt)
	}
}

func ptr[T any](v T) *T {
	return &v
}

// Short IDs are unique across the whole cache (DESIGN.md §3.1), so these IDs pair each listed
// task with one that isn't listed by default: a done task, and a gone task
const (
	listedFirstID  = "3333333333333333333333333c123456" // order 1; shares "123456" with the gone task
	listedSecondID = "1111111111111111111111111aabcdef" // order 2; shares "abcdef" with the done task
	listedDoneID   = "2222222222222222222222222babcdef" // order 3, done
	listedNoOrder  = "555555555555555555555555555fffff" // unordered, so listed last
	listedGoneID   = "4444444444444444444444444d123456" // order 0, but gone
)

// seedTaskList fills the task cache: two full refreshes, the second without the gone task
func seedTaskList(t *testing.T, dbPath string) {
	t.Helper()

	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()

	edited := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	task := func(id, name string, order *float64, done bool) store.Task {
		return store.Task{ContextID: store.DefaultContextID, PageID: id, Name: name, SortOrder: order, Done: done, LastEditedTime: edited}
	}
	gone := task(listedGoneID, "Gone", ptr(0.0), false)
	kept := []store.Task{
		task(listedSecondID, "Second", ptr(2.0), false),
		task(listedNoOrder, "No order", nil, false),
		task(listedDoneID, "Done", ptr(3.0), true),
		task(listedFirstID, "First", ptr(1.0), false),
	}

	if _, err := st.RefreshTasks(context.Background(), append(slices.Clone(kept), gone), edited, true); err != nil {
		t.Fatalf("first RefreshTasks(): %v", err)
	}
	if _, err := st.RefreshTasks(context.Background(), kept, edited.Add(time.Hour), true); err != nil {
		t.Fatalf("second RefreshTasks(): %v", err)
	}
}

type taskListOutput struct {
	Tasks []struct {
		PageID  string   `json:"page_id"`
		ShortID string   `json:"short_id"`
		Order   *float64 `json:"order"`
		Name    string   `json:"name"`
		Done    bool     `json:"done"`
	} `json:"tasks"`
	Truncated *bool `json:"truncated"`
	Warnings  []any `json:"warnings"`
}

type listedTask struct {
	pageID  string
	shortID string
}

func TestRun_TaskList(t *testing.T) {
	first := listedTask{listedFirstID, "c123456"}
	second := listedTask{listedSecondID, "aabcdef"}
	done := listedTask{listedDoneID, "babcdef"}
	noOrder := listedTask{listedNoOrder, "5fffff"}

	tests := []struct {
		name          string
		args          []string
		want          []listedTask
		wantTruncated bool
	}{
		// Short IDs are 7 characters for first and second even though the tasks they
		// share a suffix with aren't listed
		{"open_by_default", nil, []listedTask{first, second, noOrder}, false},
		{"all_adds_done", []string{"--all"}, []listedTask{first, second, done, noOrder}, false},
		{"limit_truncates", []string{"--limit", "2"}, []listedTask{first, second}, true},
		{"limit_one", []string{"--limit", "1"}, []listedTask{first}, true},
		// Exactly as many as match isn't truncated
		{"limit_same_as_matching", []string{"--limit", "3"}, []listedTask{first, second, noOrder}, false},
		{"limit_more_than_matching", []string{"--limit", "50"}, []listedTask{first, second, noOrder}, false},
		{"limit_with_all", []string{"--all", "--limit", "3"}, []listedTask{first, second, done}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			seedTaskList(t, dbPath)

			args := append([]string{"task", "list"}, tc.args...)
			code, stdout, stderr := runTT(t, args...)
			if code != 0 {
				t.Fatalf("Run(%v) = %d, want 0 (stderr: %q)", args, code, stderr)
			}

			var got taskListOutput
			if err := json.Unmarshal(stdout, &got); err != nil {
				t.Fatalf("unable to decode task list document %q: %v", stdout, err)
			}

			gotTasks := make([]listedTask, len(got.Tasks))
			for i, task := range got.Tasks {
				gotTasks[i] = listedTask{task.PageID, task.ShortID}
			}
			if !slices.Equal(gotTasks, tc.want) {
				t.Errorf("tasks (page ID, short ID) = %v, want %v", gotTasks, tc.want)
			}

			if got.Truncated == nil {
				t.Errorf("truncated missing or null, want %v", tc.wantTruncated)
			} else if *got.Truncated != tc.wantTruncated {
				t.Errorf("truncated = %v, want %v", *got.Truncated, tc.wantTruncated)
			}
			if got.Warnings == nil || len(got.Warnings) != 0 {
				t.Errorf("warnings = %v, want an empty array", got.Warnings)
			}
		})
	}
}

// Without --limit every matching task is listed (DESIGN.md §8). 25 tasks is more
// than any small default would allow
func TestRun_TaskList_NoLimitListsEverything(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("TT_DB", dbPath)

	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", dbPath, err)
	}
	edited := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	var tasks []store.Task
	for i := range 25 {
		id := fmt.Sprintf("%032d", i)
		tasks = append(tasks, store.Task{ContextID: store.DefaultContextID, PageID: id, Name: id, SortOrder: ptr(float64(i)), LastEditedTime: edited})
	}
	_, err = st.RefreshTasks(context.Background(), tasks, edited, true)
	_ = st.Close()
	if err != nil {
		t.Fatalf("RefreshTasks(): %v", err)
	}

	code, stdout, stderr := runTT(t, "task", "list")
	if code != 0 {
		t.Fatalf("Run() = %d, want 0 (stderr: %q)", code, stderr)
	}

	var got taskListOutput
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("unable to decode task list document %q: %v", stdout, err)
	}

	if len(got.Tasks) != 25 {
		t.Errorf("got %d tasks, want all 25", len(got.Tasks))
	}
	if got.Truncated == nil || *got.Truncated {
		t.Error("truncated missing, null or true, want false")
	}
}

func TestRun_TaskList_EmptyCache(t *testing.T) {
	t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))

	code, stdout, stderr := runTT(t, "task", "list")
	if code != 0 {
		t.Fatalf("Run() = %d, want 0 (stderr: %q)", code, stderr)
	}

	var got taskListOutput
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("unable to decode task list document %q: %v", stdout, err)
	}

	// An empty cache is an empty array, not null
	if got.Tasks == nil || len(got.Tasks) != 0 {
		t.Errorf("tasks = %v, want an empty array", got.Tasks)
	}
	if got.Truncated == nil || *got.Truncated {
		t.Errorf("truncated missing, null or true, want false")
	}
}

func TestRun_TaskList_Errors(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantExit int
		wantCode clierr.ErrorCode
	}{
		{"extra_argument", []string{"task", "list", "everything"}, 2, clierr.Usage},
		// runTT adds --output json at the end, but flags after a bad one aren't parsed,
		// so it comes first here to get the error as JSON
		{"limit_not_a_number", []string{"--output", "json", "task", "list", "--limit", "ten"}, 2, clierr.Usage},
		// --limit must be at least 1 (DESIGN.md §8)
		{"limit_zero", []string{"task", "list", "--limit", "0"}, 2, clierr.Usage},
		{"limit_negative", []string{"task", "list", "--limit", "-1"}, 2, clierr.Usage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))

			code, stdout, stderr := runTT(t, tc.args...)
			if code != tc.wantExit {
				t.Errorf("Run(%v) = %d, want %d (stderr: %q)", tc.args, code, tc.wantExit, stderr)
			}
			if len(stdout) != 0 {
				t.Errorf("Run(%v) wrote to stdout on failure: %q", tc.args, stdout)
			}

			var doc struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr, err)
			}
			if doc.Error.Code != string(tc.wantCode) {
				t.Errorf("error code want %q, got %q", tc.wantCode, doc.Error.Code)
			}
		})
	}
}
