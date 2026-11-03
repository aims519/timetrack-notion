package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestCacheState_ReadsTimestamps(t *testing.T) {
	// Distinct values per column, so a mix-up between columns is caught
	tasksAt := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	failedAt := time.Date(2026, 11, 2, 9, 5, 0, 0, time.UTC)
	fullAt := time.Date(2026, 11, 1, 18, 30, 0, 0, time.UTC)
	projectsAt := time.Date(2026, 11, 2, 8, 45, 0, 0, time.UTC)
	withNanos := time.Date(2026, 11, 2, 9, 0, 1, 123456789, time.UTC)

	tests := []struct {
		name         string
		tasks        *time.Time
		tasksFailed  *time.Time
		tasksFull    *time.Time
		projects     *time.Time
		wantNotFound bool
	}{
		{
			name: "never refreshed",
		},
		{
			name:        "every timestamp set",
			tasks:       &tasksAt,
			tasksFailed: &failedAt,
			tasksFull:   &fullAt,
			projects:    &projectsAt,
		},
		{
			name:  "only an incremental task refresh",
			tasks: &tasksAt,
		},
		{
			name:        "only a failed refresh",
			tasksFailed: &failedAt,
		},
		{
			name:      "full refresh without projects",
			tasks:     &tasksAt,
			tasksFull: &fullAt,
		},
		// Edge cases
		{
			name:  "nanoseconds kept",
			tasks: &withNanos,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			_, err := st.connectionPool.ExecContext(t.Context(), `
				UPDATE cache_state
				SET tasks_refreshed_at = ?,
					tasks_refresh_failed_at = ?,
					tasks_full_refreshed_at = ?,
					projects_refreshed_at = ?
				WHERE context_id = ?`,
				dbTimeOrNull(tc.tasks), dbTimeOrNull(tc.tasksFailed), dbTimeOrNull(tc.tasksFull), dbTimeOrNull(tc.projects), testContextID)
			if err != nil {
				t.Fatalf("update cache_state failed: %v", err)
			}

			got, err := st.CacheState(t.Context())
			if err != nil {
				t.Fatalf("CacheState() want nil error, got %v", err)
			}

			if got.ContextID != testContextID {
				t.Errorf("ContextID = %q, want %q", got.ContextID, testContextID)
			}

			checkTimePtr(t, "TasksRefreshedAt", got.TasksRefreshedAt, tc.tasks)
			checkTimePtr(t, "TasksRefreshFailedAt", got.TasksRefreshFailedAt, tc.tasksFailed)
			checkTimePtr(t, "TasksFullRefreshedAt", got.TasksFullRefreshedAt, tc.tasksFull)
			checkTimePtr(t, "ProjectsRefreshedAt", got.ProjectsRefreshedAt, tc.projects)
		})
	}
}

func TestCacheState_MissingRow(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	// The migration always creates the row, so it can only be missing if something
	// outside tt changed the database
	_, err := st.connectionPool.ExecContext(t.Context(), "DELETE FROM cache_state WHERE context_id = ?", testContextID)
	if err != nil {
		t.Fatalf("delete cache_state failed: %v", err)
	}

	_, err = st.CacheState(t.Context())
	if err == nil {
		t.Fatal("CacheState() want error, got nil")
	}

	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}
	if ce.Code != clierr.DatabaseUnusable {
		t.Errorf("error code = %q, want %q", ce.Code, clierr.DatabaseUnusable)
	}
}

func TestCacheState_UnparseableTimestamp(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	_, err := st.connectionPool.ExecContext(t.Context(), "UPDATE cache_state SET tasks_refreshed_at = ? WHERE context_id = ?", "yesterday", testContextID)
	if err != nil {
		t.Fatalf("update cache_state failed: %v", err)
	}

	_, err = st.CacheState(t.Context())
	if err == nil {
		t.Fatal("CacheState() want error for an unparseable timestamp, got nil")
	}

	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}
	if ce.Code != clierr.Internal {
		t.Errorf("error code = %q, want %q", ce.Code, clierr.Internal)
	}

	// Check details as they appear in the JSON error document, which is the contract (§8),
	// so the test doesn't depend on how the value is held in the map
	b, err := json.Marshal(ce.Details)
	if err != nil {
		t.Fatalf("marshalling details: %v", err)
	}
	var details map[string]any
	if err := json.Unmarshal(b, &details); err != nil {
		t.Fatalf("unmarshalling details: %v", err)
	}
	if got, want := details["tasks_refreshed_at"], "yesterday"; got != want {
		t.Errorf("details[%q] = %v, want %q", "tasks_refreshed_at", got, want)
	}
}

// dbTimeOrNull returns t in the database's time format, or nil (stored as NULL) if t is nil
func dbTimeOrNull(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ToDBString(*t)
}

// checkTimePtr reports an error if got and want aren't both nil or the same instant in UTC
func checkTimePtr(t *testing.T, field string, got, want *time.Time) {
	t.Helper()

	switch {
	case want == nil && got == nil:
		return
	case want == nil:
		t.Errorf("%s = %v, want nil", field, *got)
	case got == nil:
		t.Errorf("%s = nil, want %v", field, *want)
	case !got.Equal(*want):
		t.Errorf("%s = %v, want %v", field, *got, *want)
	case got.Location() != time.UTC:
		t.Errorf("%s location = %v, want UTC", field, got.Location())
	}
}

func TestRefreshTasks_WritesTask(t *testing.T) {
	edited := time.Date(2026, 11, 2, 9, 15, 0, 0, time.UTC)
	plusOne := time.FixedZone("UTC+1", 60*60)

	tests := []struct {
		name     string
		task     Task
		wantTime string // last_edited_time as stored
	}{
		{
			name: "every field set",
			task: Task{
				ContextID: testContextID, PageID: "page1", Name: "Write tests",
				ProjectPageID: ptr("proj1"), SortOrder: ptr(4.5), Done: true, LastEditedTime: edited,
			},
			wantTime: "2026-11-02T09:15:00.000000000Z",
		},
		{
			name: "no project or order",
			task: Task{
				ContextID: testContextID, PageID: "page1", Name: "Write tests", LastEditedTime: edited,
			},
			wantTime: "2026-11-02T09:15:00.000000000Z",
		},
		// Edge cases
		{
			name: "order of zero isn't stored as no order",
			task: Task{
				ContextID: testContextID, PageID: "page1", Name: "Write tests", SortOrder: ptr(0.0), LastEditedTime: edited,
			},
			wantTime: "2026-11-02T09:15:00.000000000Z",
		},
		{
			name: "untitled task",
			task: Task{
				ContextID: testContextID, PageID: "page1", Name: "", LastEditedTime: edited,
			},
			wantTime: "2026-11-02T09:15:00.000000000Z",
		},
		{
			name: "last edited time not in UTC is stored in UTC",
			task: Task{
				ContextID: testContextID, PageID: "page1", Name: "Write tests",
				LastEditedTime: time.Date(2026, 11, 2, 10, 15, 0, 0, plusOne),
			},
			wantTime: "2026-11-02T09:15:00.000000000Z",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			if err := st.RefreshTasks(t.Context(), []Task{tc.task}, refreshNow, false); err != nil {
				t.Fatalf("RefreshTasks() want nil error, got %v", err)
			}

			rows := readCachedTasks(t, st)
			if len(rows) != 1 {
				t.Fatalf("got %d cached tasks, want 1", len(rows))
			}
			got, ok := rows[tc.task.PageID]
			if !ok {
				t.Fatalf("no cached task with page_id %q", tc.task.PageID)
			}

			checkCachedTask(t, got, tc.task, tc.wantTime)
			if got.goneAt != nil {
				t.Errorf("gone_at = %q, want NULL", *got.goneAt)
			}
		})
	}
}

func TestRefreshTasks_UpdatesExistingTask(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	_, err := st.connectionPool.ExecContext(t.Context(), `
		INSERT INTO tasks(context_id, page_id, name, project_page_id, sort_order, done, last_edited_time)
		VALUES (?, 'page1', 'Old name', 'proj1', 1.0, 0, ?)`,
		testContextID, ToDBString(refreshEarlier))
	if err != nil {
		t.Fatalf("insert task failed: %v", err)
	}

	// Every field changes, including project and order going back to no value
	updated := Task{
		ContextID: testContextID, PageID: "page1", Name: "New name",
		ProjectPageID: nil, SortOrder: nil, Done: true, LastEditedTime: refreshNow,
	}
	if err := st.RefreshTasks(t.Context(), []Task{updated}, refreshNow, false); err != nil {
		t.Fatalf("RefreshTasks() want nil error, got %v", err)
	}

	rows := readCachedTasks(t, st)
	if len(rows) != 1 {
		t.Fatalf("got %d cached tasks, want 1", len(rows))
	}
	checkCachedTask(t, rows["page1"], updated, ToDBString(refreshNow))
}

func TestRefreshTasks_GoneAt(t *testing.T) {
	type existing struct {
		pageID string
		goneAt *time.Time
	}

	tests := []struct {
		name     string
		existing []existing
		returned []string // page IDs of the tasks passed to RefreshTasks
		full     bool
		want     map[string]*time.Time // gone_at for each page ID; nil means not gone
	}{
		{
			name:     "incremental leaves missing tasks alone",
			existing: []existing{{"a", nil}, {"b", nil}, {"c", nil}},
			returned: []string{"a"},
			want:     map[string]*time.Time{"a": nil, "b": nil, "c": nil},
		},
		{
			name:     "incremental clears gone_at of a returned task",
			existing: []existing{{"a", &refreshEarlier}},
			returned: []string{"a"},
			want:     map[string]*time.Time{"a": nil},
		},
		{
			name:     "incremental with no tasks changes nothing",
			existing: []existing{{"a", nil}, {"b", &refreshEarlier}},
			returned: nil,
			want:     map[string]*time.Time{"a": nil, "b": &refreshEarlier},
		},
		{
			name:     "full marks missing tasks gone",
			existing: []existing{{"a", nil}, {"b", nil}, {"c", nil}},
			returned: []string{"a"},
			full:     true,
			want:     map[string]*time.Time{"a": nil, "b": &refreshNow, "c": &refreshNow},
		},
		{
			name:     "full keeps every returned task",
			existing: []existing{{"a", nil}, {"b", nil}, {"c", nil}},
			returned: []string{"a", "b", "c"},
			full:     true,
			want:     map[string]*time.Time{"a": nil, "b": nil, "c": nil},
		},
		{
			name:     "full clears gone_at of a returned task",
			existing: []existing{{"a", &refreshEarlier}},
			returned: []string{"a"},
			full:     true,
			want:     map[string]*time.Time{"a": nil},
		},
		{
			name:     "full adds new tasks and marks missing ones gone",
			existing: []existing{{"a", nil}},
			returned: []string{"b", "c"},
			full:     true,
			want:     map[string]*time.Time{"a": &refreshNow, "b": nil, "c": nil},
		},
		// Edge cases
		{
			name:     "full keeps the original gone_at of a task already gone",
			existing: []existing{{"a", nil}, {"b", &refreshEarlier}},
			returned: []string{"a"},
			full:     true,
			want:     map[string]*time.Time{"a": nil, "b": &refreshEarlier},
		},
		{
			name:     "full with no tasks marks every task gone",
			existing: []existing{{"a", nil}, {"b", nil}},
			returned: nil,
			full:     true,
			want:     map[string]*time.Time{"a": &refreshNow, "b": &refreshNow},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			for _, e := range tc.existing {
				_, err := st.connectionPool.ExecContext(t.Context(), `
					INSERT INTO tasks(context_id, page_id, name, done, last_edited_time, gone_at)
					VALUES (?, ?, 'Existing task', 0, ?, ?)`,
					testContextID, e.pageID, ToDBString(refreshEarlier), dbTimeOrNull(e.goneAt))
				if err != nil {
					t.Fatalf("insert task %q failed: %v", e.pageID, err)
				}
			}

			tasks := make([]Task, len(tc.returned))
			for i, id := range tc.returned {
				tasks[i] = Task{ContextID: testContextID, PageID: id, Name: "Returned task", LastEditedTime: refreshNow}
			}

			if err := st.RefreshTasks(t.Context(), tasks, refreshNow, tc.full); err != nil {
				t.Fatalf("RefreshTasks() want nil error, got %v", err)
			}

			rows := readCachedTasks(t, st)
			if len(rows) != len(tc.want) {
				t.Errorf("got %d cached tasks, want %d", len(rows), len(tc.want))
			}
			for id, wantGone := range tc.want {
				row, ok := rows[id]
				if !ok {
					t.Errorf("no cached task with page_id %q", id)
					continue
				}
				gotGone, err := FromNullableDBString(row.goneAt)
				if err != nil {
					t.Errorf("task %q: gone_at %q doesn't parse: %v", id, *row.goneAt, err)
					continue
				}
				checkTimePtr(t, "task "+id+" gone_at", gotGone, wantGone)
			}
		})
	}
}

func TestRefreshTasks_CacheState(t *testing.T) {
	projectsAt := time.Date(2026, 11, 2, 8, 45, 0, 0, time.UTC)

	tests := []struct {
		name         string
		priorRefresh *time.Time
		priorFull    *time.Time
		full         bool
		tasks        []Task
		wantFull     *time.Time
	}{
		{
			name:     "first incremental refresh",
			full:     false,
			tasks:    []Task{{ContextID: testContextID, PageID: "a", LastEditedTime: refreshNow}},
			wantFull: nil,
		},
		{
			name:         "incremental refresh keeps the last full refresh time",
			priorRefresh: &refreshEarlier,
			priorFull:    &refreshEarlier,
			full:         false,
			tasks:        []Task{{ContextID: testContextID, PageID: "a", LastEditedTime: refreshNow}},
			wantFull:     &refreshEarlier,
		},
		{
			name:         "full refresh sets both times",
			priorRefresh: &refreshEarlier,
			priorFull:    &refreshEarlier,
			full:         true,
			tasks:        []Task{{ContextID: testContextID, PageID: "a", LastEditedTime: refreshNow}},
			wantFull:     &refreshNow,
		},
		// Edge cases
		{
			name:         "incremental refresh with no tasks is still recorded",
			priorRefresh: &refreshEarlier,
			full:         false,
			tasks:        nil,
			wantFull:     nil,
		},
		{
			name:     "full refresh with no tasks is still recorded",
			full:     true,
			tasks:    nil,
			wantFull: &refreshNow,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			_, err := st.connectionPool.ExecContext(t.Context(), `
				UPDATE cache_state
				SET tasks_refreshed_at = ?,
					tasks_full_refreshed_at = ?,
					projects_refreshed_at = ?
				WHERE context_id = ?`,
				dbTimeOrNull(tc.priorRefresh), dbTimeOrNull(tc.priorFull), ToDBString(projectsAt), testContextID)
			if err != nil {
				t.Fatalf("update cache_state failed: %v", err)
			}

			if err := st.RefreshTasks(t.Context(), tc.tasks, refreshNow, tc.full); err != nil {
				t.Fatalf("RefreshTasks() want nil error, got %v", err)
			}

			// Reading back through CacheState also checks the times are stored in a format it can parse,
			// so the next refresh can read them
			got, err := st.CacheState(t.Context())
			if err != nil {
				t.Fatalf("CacheState() after RefreshTasks() want nil error, got %v", err)
			}

			checkTimePtr(t, "TasksRefreshedAt", got.TasksRefreshedAt, &refreshNow)
			checkTimePtr(t, "TasksFullRefreshedAt", got.TasksFullRefreshedAt, tc.wantFull)
			checkTimePtr(t, "ProjectsRefreshedAt", got.ProjectsRefreshedAt, &projectsAt)
		})
	}
}

func TestRefreshTasks_FailureChangesNothing(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	_, err := st.connectionPool.ExecContext(t.Context(), `
		INSERT INTO tasks(context_id, page_id, name, done, last_edited_time)
		VALUES (?, 'existing', 'Existing task', 0, ?)`,
		testContextID, ToDBString(refreshEarlier))
	if err != nil {
		t.Fatalf("insert task failed: %v", err)
	}

	// The second task's context doesn't exist, so its insert breaks the foreign key
	// after the first task has been written
	tasks := []Task{
		{ContextID: testContextID, PageID: "good", Name: "Good task", LastEditedTime: refreshNow},
		{ContextID: "11111111-1111-1111-1111-111111111111", PageID: "bad", Name: "Bad task", LastEditedTime: refreshNow},
	}

	err = st.RefreshTasks(t.Context(), tasks, refreshNow, true)
	if err == nil {
		t.Fatal("RefreshTasks() want error, got nil")
	}
	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}

	rows := readCachedTasks(t, st)
	if len(rows) != 1 {
		t.Errorf("got %d cached tasks, want 1 (only the existing one)", len(rows))
	}
	if row, ok := rows["existing"]; !ok {
		t.Error("existing task missing")
	} else if row.goneAt != nil {
		t.Errorf("existing task gone_at = %q, want NULL (full refresh rolled back)", *row.goneAt)
	}

	got, err := st.CacheState(t.Context())
	if err != nil {
		t.Fatalf("CacheState() want nil error, got %v", err)
	}
	checkTimePtr(t, "TasksRefreshedAt", got.TasksRefreshedAt, nil)
	checkTimePtr(t, "TasksFullRefreshedAt", got.TasksFullRefreshedAt, nil)
}

var (
	refreshNow     = time.Date(2026, 11, 3, 9, 15, 0, 0, time.UTC)
	refreshEarlier = time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
)

// cachedTask is a row of the tasks table as stored, so the tests also check the stored formats
type cachedTask struct {
	name           string
	projectPageID  *string
	sortOrder      *float64
	done           int64
	lastEditedTime string
	goneAt         *string
}

// readCachedTasks returns every row of the tasks table for the test context, by page ID
func readCachedTasks(t *testing.T, st *Store) map[string]cachedTask {
	t.Helper()

	rows, err := st.connectionPool.QueryContext(t.Context(), `
		SELECT page_id, name, project_page_id, sort_order, done, last_edited_time, gone_at
		FROM tasks
		WHERE context_id = ?`, testContextID)
	if err != nil {
		t.Fatalf("query tasks failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	result := map[string]cachedTask{}
	for rows.Next() {
		var id string
		var c cachedTask
		if err := rows.Scan(&id, &c.name, &c.projectPageID, &c.sortOrder, &c.done, &c.lastEditedTime, &c.goneAt); err != nil {
			t.Fatalf("scan task failed: %v", err)
		}
		result[id] = c
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading tasks failed: %v", err)
	}

	return result
}

// checkCachedTask reports an error for each field of got that doesn't match want
func checkCachedTask(t *testing.T, got cachedTask, want Task, wantTime string) {
	t.Helper()

	if got.name != want.Name {
		t.Errorf("name = %q, want %q", got.name, want.Name)
	}

	switch {
	case want.ProjectPageID == nil && got.projectPageID != nil:
		t.Errorf("project_page_id = %q, want NULL", *got.projectPageID)
	case want.ProjectPageID != nil && got.projectPageID == nil:
		t.Errorf("project_page_id = NULL, want %q", *want.ProjectPageID)
	case want.ProjectPageID != nil && *got.projectPageID != *want.ProjectPageID:
		t.Errorf("project_page_id = %q, want %q", *got.projectPageID, *want.ProjectPageID)
	}

	switch {
	case want.SortOrder == nil && got.sortOrder != nil:
		t.Errorf("sort_order = %v, want NULL", *got.sortOrder)
	case want.SortOrder != nil && got.sortOrder == nil:
		t.Errorf("sort_order = NULL, want %v", *want.SortOrder)
	case want.SortOrder != nil && *got.sortOrder != *want.SortOrder:
		t.Errorf("sort_order = %v, want %v", *got.sortOrder, *want.SortOrder)
	}

	wantDone := int64(0)
	if want.Done {
		wantDone = 1
	}
	if got.done != wantDone {
		t.Errorf("done = %d, want %d", got.done, wantDone)
	}

	if got.lastEditedTime != wantTime {
		t.Errorf("last_edited_time = %q, want %q", got.lastEditedTime, wantTime)
	}
}

func ptr[T any](v T) *T {
	return &v
}
