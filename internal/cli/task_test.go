package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
		{"project_empty", []string{"task", "list", "--project", ""}, 2, clierr.Usage},
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

// The project in each task item is part of the JSON contract (DESIGN.md §8): the project's
// page ID and name when it's cached, and null otherwise
func TestRun_TaskList_Project(t *testing.T) {
	const (
		inKeptID        = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa1"
		inGoneID        = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa2"
		notCachedID     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa3"
		noProjectID     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa4"
		keptProject     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb1"
		goneProject     = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb2"
		uncachedProject = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb3"
	)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("TT_DB", dbPath)

	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", dbPath, err)
	}
	refreshed := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	kept := store.Project{ContextID: store.DefaultContextID, PageID: keptProject, Name: "Time tracker"}
	gone := store.Project{ContextID: store.DefaultContextID, PageID: goneProject, Name: "Deleted project"}
	task := func(id string, project *string, order float64) store.Task {
		return store.Task{ContextID: store.DefaultContextID, PageID: id, Name: "Task " + id[len(id)-1:], ProjectPageID: project, SortOrder: ptr(order), LastEditedTime: refreshed}
	}

	// The second projects refresh leaves out the gone project, so it's marked gone
	_, err = st.RefreshProjects(context.Background(), []store.Project{kept, gone}, refreshed)
	if err == nil {
		_, err = st.RefreshProjects(context.Background(), []store.Project{kept}, refreshed.Add(time.Hour))
	}
	if err == nil {
		_, err = st.RefreshTasks(context.Background(), []store.Task{
			task(inKeptID, ptr(keptProject), 1),
			task(inGoneID, ptr(goneProject), 2),
			task(notCachedID, ptr(uncachedProject), 3),
			task(noProjectID, nil, 4),
		}, refreshed, true)
	}
	_ = st.Close()
	if err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}

	code, stdout, stderr := runTT(t, "task", "list")
	if code != 0 {
		t.Fatalf("Run() = %d, want 0 (stderr: %q)", code, stderr)
	}

	// Decoded as raw JSON per field, so a missing "project" key isn't mistaken for null
	var got struct {
		Tasks []map[string]json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("unable to decode task list document %q: %v", stdout, err)
	}

	gotProjects := map[string]json.RawMessage{}
	for _, item := range got.Tasks {
		var pageID string
		if err := json.Unmarshal(item["page_id"], &pageID); err != nil {
			t.Fatalf("decoding page_id %q: %v", item["page_id"], err)
		}
		project, ok := item["project"]
		if !ok {
			t.Errorf("task %s has no project key, want one, even if null", pageID)
			continue
		}
		gotProjects[pageID] = project
	}

	tests := []struct {
		name   string
		pageID string
		want   string // the task's "project" value as JSON
	}{
		{"cached project", inKeptID, `{"page_id":"` + keptProject + `","name":"Time tracker"}`},
		// The task is still listed, without the deleted project's name
		{"project gone", inGoneID, `null`},
		{"project not cached", notCachedID, `null`},
		{"no project", noProjectID, `null`},
	}

	if len(got.Tasks) != len(tests) {
		t.Errorf("got %d tasks, want %d", len(got.Tasks), len(tests))
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, ok := gotProjects[tc.pageID]
			if !ok {
				t.Fatalf("task %s not listed", tc.pageID)
			}

			// Compared as decoded values, so spacing and key order in the output don't matter
			var gotValue, wantValue any
			if err := json.Unmarshal(raw, &gotValue); err != nil {
				t.Fatalf("decoding project %q: %v", raw, err)
			}
			if err := json.Unmarshal([]byte(tc.want), &wantValue); err != nil {
				t.Fatalf("decoding want %q: %v", tc.want, err)
			}
			if !reflect.DeepEqual(gotValue, wantValue) {
				t.Errorf("project = %s, want %s", raw, tc.want)
			}
		})
	}
}

const (
	filterSabbaticalProject = "cccccccccccccccccccccccccccccc01"
	filterTrackerProject    = "cccccccccccccccccccccccccccccc02"
	filterAdminProject      = "cccccccccccccccccccccccccccccc03"
	filterAdminLowerProject = "cccccccccccccccccccccccccccccc04"
	filterDeletedProject    = "cccccccccccccccccccccccccccccc05"

	filterSabbaticalTask = "dddddddddddddddddddddddddddddd01"
	filterTrackerTask    = "dddddddddddddddddddddddddddddd02"
	filterAdminTask      = "dddddddddddddddddddddddddddddd03"
	filterNoProjectTask  = "dddddddddddddddddddddddddddddd04"
)

// seedProjectFilter caches one open task in each of Sabbatical, Tracker and Admin, and one with
// no project. "Admin" and "admin" are two projects, and "Deleted" is gone. defaultProject is the
// context's default project, or nil for none
func seedProjectFilter(t *testing.T, dbPath string, defaultProject *string) {
	t.Helper()

	st, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()

	refreshed := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	project := func(id, name string) store.Project {
		return store.Project{ContextID: store.DefaultContextID, PageID: id, Name: name}
	}
	task := func(id string, project *string, order float64) store.Task {
		return store.Task{ContextID: store.DefaultContextID, PageID: id, Name: "Task " + id[len(id)-2:], ProjectPageID: project, SortOrder: ptr(order), LastEditedTime: refreshed}
	}
	live := []store.Project{
		project(filterSabbaticalProject, "Sabbatical"),
		project(filterTrackerProject, "Tracker"),
		project(filterAdminProject, "Admin"),
		project(filterAdminLowerProject, "admin"),
	}

	// The second projects refresh leaves out "Deleted", so it's marked gone
	if _, err := st.RefreshProjects(context.Background(), append(slices.Clone(live), project(filterDeletedProject, "Deleted")), refreshed); err != nil {
		t.Fatalf("first RefreshProjects(): %v", err)
	}
	if _, err := st.RefreshProjects(context.Background(), live, refreshed.Add(time.Hour)); err != nil {
		t.Fatalf("second RefreshProjects(): %v", err)
	}
	if _, err := st.RefreshTasks(context.Background(), []store.Task{
		task(filterSabbaticalTask, ptr(filterSabbaticalProject), 1),
		task(filterTrackerTask, ptr(filterTrackerProject), 2),
		task(filterAdminTask, ptr(filterAdminProject), 3),
		task(filterNoProjectTask, nil, 4),
	}, refreshed, true); err != nil {
		t.Fatalf("RefreshTasks(): %v", err)
	}

	nc, err := st.NotionContext(context.Background(), store.DefaultContextID)
	if err != nil {
		t.Fatalf("NotionContext(): %v", err)
	}
	nc.DefaultProjectPageID = defaultProject
	if err := st.SaveNotionContext(context.Background(), nc); err != nil {
		t.Fatalf("SaveNotionContext(): %v", err)
	}
}

func TestRun_TaskList_ProjectFilter(t *testing.T) {
	tests := []struct {
		name           string
		defaultProject *string
		args           []string
		want           []string // page IDs, in list order
	}{
		{
			name: "no flag and no default lists every project",
			want: []string{filterSabbaticalTask, filterTrackerTask, filterAdminTask, filterNoProjectTask},
		},
		{
			name: "project flag",
			args: []string{"--project", "Sabbatical"},
			want: []string{filterSabbaticalTask},
		},
		{
			name: "project flag ignores case",
			args: []string{"--project", "tRACKER"},
			want: []string{filterTrackerTask},
		},
		{
			name:           "default project without the flag",
			defaultProject: ptr(filterSabbaticalProject),
			want:           []string{filterSabbaticalTask},
		},
		{
			name:           "project flag overrides the default project",
			defaultProject: ptr(filterSabbaticalProject),
			args:           []string{"--project", "Tracker"},
			want:           []string{filterTrackerTask},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			seedProjectFilter(t, dbPath, tc.defaultProject)

			args := append([]string{"task", "list"}, tc.args...)
			code, stdout, stderr := runTT(t, args...)
			if code != 0 {
				t.Fatalf("Run(%v) = %d, want 0 (stderr: %q)", args, code, stderr)
			}

			var got taskListOutput
			if err := json.Unmarshal(stdout, &got); err != nil {
				t.Fatalf("unable to decode task list document %q: %v", stdout, err)
			}

			ids := make([]string, len(got.Tasks))
			for i, task := range got.Tasks {
				ids[i] = task.PageID
			}
			if !slices.Equal(ids, tc.want) {
				t.Errorf("page IDs = %v, want %v", ids, tc.want)
			}
		})
	}
}

func TestRun_TaskList_ProjectFilter_Errors(t *testing.T) {
	type candidate struct {
		PageID string `json:"page_id"`
		Name   string `json:"name"`
	}

	tests := []struct {
		name           string
		project        string
		wantCode       clierr.ErrorCode
		wantCandidates []candidate // in the order given, by name; nil for not_found
	}{
		{
			name:     "no project with the name",
			project:  "Holiday",
			wantCode: clierr.NotFound,
		},
		{
			// Ignoring case, "admin" matches both "Admin" and "admin"
			name:     "several projects with the name",
			project:  "ADMIN",
			wantCode: clierr.AmbiguousSelector,
			wantCandidates: []candidate{
				{filterAdminProject, "Admin"},
				{filterAdminLowerProject, "admin"},
			},
		},
		// Edge cases
		{
			name:     "gone project",
			project:  "Deleted",
			wantCode: clierr.NotFound,
		},
		{
			name:     "part of a name",
			project:  "Sabb",
			wantCode: clierr.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)
			seedProjectFilter(t, dbPath, nil)

			code, stdout, stderr := runTT(t, "task", "list", "--project", tc.project)
			if code != 3 {
				t.Errorf("Run() = %d, want 3 (stderr: %q)", code, stderr)
			}
			if len(stdout) != 0 {
				t.Errorf("Run() wrote to stdout on failure: %q", stdout)
			}

			var doc struct {
				Error struct {
					Code    string `json:"code"`
					Details struct {
						Selector   *string     `json:"selector"`
						Candidates []candidate `json:"candidates"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr, err)
			}

			if doc.Error.Code != string(tc.wantCode) {
				t.Errorf("error code = %q, want %q", doc.Error.Code, tc.wantCode)
			}
			// The selector is the name as given, not as the project spells it
			if doc.Error.Details.Selector == nil {
				t.Errorf("details.selector missing, want %q", tc.project)
			} else if *doc.Error.Details.Selector != tc.project {
				t.Errorf("details.selector = %q, want %q", *doc.Error.Details.Selector, tc.project)
			}

			gotCandidates := doc.Error.Details.Candidates
			if !slices.Equal(gotCandidates, tc.wantCandidates) {
				t.Errorf("details.candidates = %+v, want %+v", gotCandidates, tc.wantCandidates)
			}
		})
	}
}
