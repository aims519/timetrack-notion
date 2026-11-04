package store

import (
	"path/filepath"
	"slices"
	"testing"
)

// listTask is a row written straight to the tasks table, so tests can set gone_at
// and use a second context, which RefreshTasks can't
type listTask struct {
	contextID string
	pageID    string
	name      string
	project   *string
	order     *float64
	done      bool
	gone      bool
}

const otherContextID = "11111111-1111-1111-1111-111111111111"

func insertListTasks(t *testing.T, st *Store, tasks []listTask) {
	t.Helper()

	for _, task := range tasks {
		contextID := task.contextID
		if contextID == "" {
			contextID = testContextID
		}

		var goneAt any
		if task.gone {
			goneAt = "2026-11-03T09:00:00Z"
		}

		done := 0
		if task.done {
			done = 1
		}

		_, err := st.connectionPool.ExecContext(t.Context(), `
			INSERT INTO tasks(context_id, page_id, name, project_page_id, sort_order, done, last_edited_time, gone_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			contextID, task.pageID, task.name, task.project, task.order, done, "2026-11-03T08:00:00Z", goneAt)
		if err != nil {
			t.Fatalf("insert task %q failed: %v", task.pageID, err)
		}
	}
}

func insertOtherContext(t *testing.T, st *Store) {
	t.Helper()

	_, err := st.connectionPool.ExecContext(t.Context(), `INSERT INTO contexts(id) VALUES (?)`, otherContextID)
	if err != nil {
		t.Fatalf("insert context failed: %v", err)
	}
}

func listedPageIDs(tasks []Task) []string {
	ids := make([]string, len(tasks))
	for i, task := range tasks {
		ids[i] = task.PageID
	}
	return ids
}

func TestListTasks_Filters(t *testing.T) {
	tasks := []listTask{
		{pageID: "open", name: "Open", order: ptr(1.0)},
		{pageID: "done", name: "Done", order: ptr(2.0), done: true},
		{pageID: "gone_open", name: "Gone open", order: ptr(3.0), gone: true},
		{pageID: "gone_done", name: "Gone done", order: ptr(4.0), done: true, gone: true},
		{contextID: otherContextID, pageID: "other_context", name: "Other context", order: ptr(5.0)},
	}

	tests := []struct {
		name string
		all  bool
		want []string
	}{
		// Gone tasks are never listed, done or not (DESIGN.md §4)
		{"open_only_by_default", false, []string{"open"}},
		{"all_adds_done", true, []string{"open", "done"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertOtherContext(t, st)
			insertListTasks(t, st, tasks)

			got, _, err := st.ListTasks(t.Context(), 100, tc.all)
			if err != nil {
				t.Fatalf("ListTasks() want nil error, got %v", err)
			}

			if ids := listedPageIDs(got); !slices.Equal(ids, tc.want) {
				t.Errorf("ListTasks(all=%v) = %q, want %q", tc.all, ids, tc.want)
			}
		})
	}
}

func TestListTasks_EmptyCache(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	got, truncated, err := st.ListTasks(t.Context(), 100, true)
	if err != nil {
		t.Fatalf("ListTasks() want nil error, got %v", err)
	}

	if truncated {
		t.Error("truncated = true, want false")
	}
	if len(got) != 0 {
		t.Errorf("ListTasks() = %v, want no tasks", got)
	}
}

// Order is part of the JSON contract: by Order, then name, unordered last (DESIGN.md §8)
func TestListTasks_Order(t *testing.T) {
	tests := []struct {
		name  string
		tasks []listTask
		want  []string
	}{
		{
			// Order is a number, so 10 comes after 2.3, not before it as text would
			name: "by_order_numerically",
			tasks: []listTask{
				{pageID: "ten", name: "A", order: ptr(10.0)},
				{pageID: "two_point_three", name: "B", order: ptr(2.3)},
				{pageID: "one", name: "C", order: ptr(1.0)},
			},
			want: []string{"one", "two_point_three", "ten"},
		},
		{
			// Page IDs sort the opposite way to names, so falling back to page ID or
			// insertion order would fail
			name: "same_order_by_name",
			tasks: []listTask{
				{pageID: "id_a", name: "Charlie", order: ptr(4.1)},
				{pageID: "id_c", name: "Alpha", order: ptr(4.1)},
				{pageID: "id_b", name: "Bravo", order: ptr(4.1)},
			},
			want: []string{"id_c", "id_b", "id_a"},
		},
		{
			// SQLite sorts NULL first in ascending order, so this needs handling
			name: "unordered_last",
			tasks: []listTask{
				{pageID: "unordered", name: "Alpha"},
				{pageID: "ordered", name: "Zulu", order: ptr(99.0)},
			},
			want: []string{"ordered", "unordered"},
		},
		{
			name: "unordered_by_name",
			tasks: []listTask{
				{pageID: "id_a", name: "Zulu"},
				{pageID: "id_z", name: "Alpha"},
			},
			want: []string{"id_z", "id_a"},
		},
		{
			// Names compare case-insensitively. Comparing by byte value would put
			// every upper-case letter first, giving Banana, apple, cherry
			name: "same_order_name_ignores_case",
			tasks: []listTask{
				{pageID: "id_c", name: "cherry", order: ptr(1.0)},
				{pageID: "id_b", name: "Banana", order: ptr(1.0)},
				{pageID: "id_a", name: "apple", order: ptr(1.0)},
			},
			want: []string{"id_a", "id_b", "id_c"},
		},
		{
			name: "unordered_name_ignores_case",
			tasks: []listTask{
				{pageID: "id_c", name: "cherry"},
				{pageID: "id_b", name: "Banana"},
				{pageID: "id_a", name: "apple"},
			},
			want: []string{"id_a", "id_b", "id_c"},
		},
		{
			// The project doesn't affect the order
			name: "projects_mixed",
			tasks: []listTask{
				{pageID: "p2_first", name: "A", project: ptr("project2"), order: ptr(1.0)},
				{pageID: "no_project", name: "B", order: ptr(2.0)},
				{pageID: "p1_third", name: "C", project: ptr("project1"), order: ptr(3.0)},
			},
			want: []string{"p2_first", "no_project", "p1_third"},
		},
		{
			// Done tasks are sorted in with open ones, not after them
			name: "done_sorted_with_open",
			tasks: []listTask{
				{pageID: "open_2", name: "B", order: ptr(2.0)},
				{pageID: "done_1", name: "A", order: ptr(1.0), done: true},
				{pageID: "open_3", name: "C", order: ptr(3.0)},
			},
			want: []string{"done_1", "open_2", "open_3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertListTasks(t, st, tc.tasks)

			got, _, err := st.ListTasks(t.Context(), 100, true)
			if err != nil {
				t.Fatalf("ListTasks() want nil error, got %v", err)
			}

			if ids := listedPageIDs(got); !slices.Equal(ids, tc.want) {
				t.Errorf("ListTasks() = %q, want %q", ids, tc.want)
			}
		})
	}
}

func TestListTasks_Limit(t *testing.T) {
	tasks := []listTask{
		{pageID: "one", name: "A", order: ptr(1.0)},
		{pageID: "two", name: "B", order: ptr(2.0)},
		{pageID: "three", name: "C", order: ptr(3.0)},
		// Done tasks don't count towards the limit unless they're listed
		{pageID: "done", name: "D", order: ptr(0.5), done: true},
	}

	tests := []struct {
		name          string
		limit         int
		all           bool
		want          []string
		wantTruncated bool
	}{
		{"fewer_than_matching", 2, false, []string{"one", "two"}, true},
		// Exactly as many as match isn't truncated: there's nothing more to see
		{"same_as_matching", 3, false, []string{"one", "two", "three"}, false},
		{"more_than_matching", 10, false, []string{"one", "two", "three"}, false},
		{"one", 1, false, []string{"one"}, true},
		// The first rows in order are kept, so with --all the done task comes first
		{"with_all", 2, true, []string{"done", "one"}, true},
		// The done task only counts when it's listed, so 3 open tasks fill a limit of 3
		{"with_all_same_as_matching", 4, true, []string{"done", "one", "two", "three"}, false},
		// NoLimit: every matching row, and never truncated
		{"no_limit", NoLimit, false, []string{"one", "two", "three"}, false},
		{"no_limit_with_all", NoLimit, true, []string{"done", "one", "two", "three"}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertListTasks(t, st, tasks)

			got, truncated, err := st.ListTasks(t.Context(), tc.limit, tc.all)
			if err != nil {
				t.Fatalf("ListTasks() want nil error, got %v", err)
			}

			if ids := listedPageIDs(got); !slices.Equal(ids, tc.want) {
				t.Errorf("ListTasks(limit=%d, all=%v) = %q, want %q", tc.limit, tc.all, ids, tc.want)
			}
			if truncated != tc.wantTruncated {
				t.Errorf("ListTasks(limit=%d, all=%v) truncated = %v, want %v", tc.limit, tc.all, truncated, tc.wantTruncated)
			}
		})
	}
}

// Rows a filter leaves out aren't "more rows matched", so they never make the list truncated
func TestListTasks_Limit_FilteredRowsNotTruncated(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
	insertOtherContext(t, st)
	insertListTasks(t, st, []listTask{
		{pageID: "one", name: "A", order: ptr(1.0)},
		{pageID: "two", name: "B", order: ptr(2.0)},
		{pageID: "done", name: "C", order: ptr(3.0), done: true},
		{pageID: "gone", name: "D", order: ptr(4.0), gone: true},
		{contextID: otherContextID, pageID: "other_context", name: "E", order: ptr(5.0)},
	})

	got, truncated, err := st.ListTasks(t.Context(), 2, false)
	if err != nil {
		t.Fatalf("ListTasks() want nil error, got %v", err)
	}

	if ids := listedPageIDs(got); !slices.Equal(ids, []string{"one", "two"}) {
		t.Errorf("ListTasks() = %q, want %q", ids, []string{"one", "two"})
	}
	if truncated {
		t.Error("truncated = true, want false: only filtered-out rows were left")
	}
}

func TestListTasks_ReadsFields(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
	insertListTasks(t, st, []listTask{
		{pageID: "every_field", name: `Write "tests" ✓`, project: ptr("project1"), order: ptr(4.1), done: true},
		{pageID: "nullable_fields_null", name: "No project or order"},
	})

	got, _, err := st.ListTasks(t.Context(), 100, true)
	if err != nil {
		t.Fatalf("ListTasks() want nil error, got %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListTasks() returned %d tasks, want 2: %v", len(got), got)
	}

	// Picked by ID, so this test doesn't depend on the order tested above
	byID := map[string]Task{}
	for _, task := range got {
		byID[task.PageID] = task
	}
	full, empty := byID["every_field"], byID["nullable_fields_null"]

	if full.ContextID != testContextID {
		t.Errorf("ContextID = %q, want %q", full.ContextID, testContextID)
	}
	if full.PageID != "every_field" {
		t.Errorf("PageID = %q, want %q", full.PageID, "every_field")
	}
	if full.Name != `Write "tests" ✓` {
		t.Errorf("Name = %q, want %q", full.Name, `Write "tests" ✓`)
	}
	if full.ProjectPageID == nil || *full.ProjectPageID != "project1" {
		t.Errorf("ProjectPageID = %v, want %q", full.ProjectPageID, "project1")
	}
	if full.SortOrder == nil || *full.SortOrder != 4.1 {
		t.Errorf("SortOrder = %v, want 4.1", full.SortOrder)
	}
	if !full.Done {
		t.Error("Done = false, want true")
	}

	if empty.ProjectPageID != nil {
		t.Errorf("ProjectPageID = %q, want nil", *empty.ProjectPageID)
	}
	if empty.SortOrder != nil {
		t.Errorf("SortOrder = %v, want nil", *empty.SortOrder)
	}
	if empty.Done {
		t.Error("Done = true, want false")
	}
}

func TestAllTaskIDs(t *testing.T) {
	tests := []struct {
		name  string
		tasks []listTask
		want  []string
	}{
		{"empty_cache", nil, nil},
		{
			// Short IDs are unique across the whole cache (DESIGN.md §3.1), so done and
			// gone tasks are included. Only the other context's tasks are left out
			name: "done_and_gone_included",
			tasks: []listTask{
				{pageID: "open", name: "A"},
				{pageID: "done", name: "B", done: true},
				{pageID: "gone", name: "C", gone: true},
				{pageID: "gone_done", name: "D", done: true, gone: true},
				{contextID: otherContextID, pageID: "other_context", name: "E"},
			},
			want: []string{"done", "gone", "gone_done", "open"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertOtherContext(t, st)
			insertListTasks(t, st, tc.tasks)

			got, err := st.AllTaskIDs(t.Context())
			if err != nil {
				t.Fatalf("AllTaskIDs() want nil error, got %v", err)
			}

			// The order isn't part of what AllTaskIDs promises
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("AllTaskIDs() = %q, want %q", got, tc.want)
			}
		})
	}
}

// listProject is a row written straight to the projects table, so tests can set gone_at
// and use a second context
type listProject struct {
	contextID string
	pageID    string
	name      string
	gone      bool
}

func insertListProjects(t *testing.T, st *Store, projects []listProject) {
	t.Helper()

	for _, p := range projects {
		contextID := p.contextID
		if contextID == "" {
			contextID = testContextID
		}

		var goneAt any
		if p.gone {
			goneAt = "2026-11-03T09:00:00Z"
		}

		_, err := st.connectionPool.ExecContext(t.Context(), `
			INSERT INTO projects(context_id, page_id, name, gone_at)
			VALUES (?, ?, ?, ?)`,
			contextID, p.pageID, p.name, goneAt)
		if err != nil {
			t.Fatalf("insert project %q failed: %v", p.pageID, err)
		}
	}
}

func TestListTasks_Project(t *testing.T) {
	tests := []struct {
		name        string
		project     *string // the task's project_page_id
		projects    []listProject
		wantProject *string // ProjectPageID; nil means none
		wantName    *string // ProjectName; nil means none
	}{
		{
			name:        "cached project",
			project:     ptr("project1"),
			projects:    []listProject{{pageID: "project1", name: "Sabbatical"}},
			wantProject: ptr("project1"),
			wantName:    ptr("Sabbatical"),
		},
		{
			name:        "no project",
			project:     nil,
			projects:    []listProject{{pageID: "project1", name: "Sabbatical"}},
			wantProject: nil,
			wantName:    nil,
		},
		{
			// A task can be refreshed before its project is
			name:        "project not cached",
			project:     ptr("project1"),
			projects:    nil,
			wantProject: ptr("project1"),
			wantName:    nil,
		},
		{
			name:    "the task's project is picked from several",
			project: ptr("project2"),
			projects: []listProject{
				{pageID: "project1", name: "Sabbatical"},
				{pageID: "project2", name: "Tracker"},
			},
			wantProject: ptr("project2"),
			wantName:    ptr("Tracker"),
		},
		// Edge cases
		{
			// The task is still listed; only the gone project's name is left out
			name:        "project gone",
			project:     ptr("project1"),
			projects:    []listProject{{pageID: "project1", name: "Sabbatical", gone: true}},
			wantProject: ptr("project1"),
			wantName:    nil,
		},
		{
			name:        "project with the same ID in another context isn't used",
			project:     ptr("project1"),
			projects:    []listProject{{contextID: otherContextID, pageID: "project1", name: "Other context"}},
			wantProject: ptr("project1"),
			wantName:    nil,
		},
		{
			// An untitled project is still a project, so its name is "" rather than none
			name:        "untitled project",
			project:     ptr("project1"),
			projects:    []listProject{{pageID: "project1", name: ""}},
			wantProject: ptr("project1"),
			wantName:    ptr(""),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
			insertOtherContext(t, st)
			insertListProjects(t, st, tc.projects)
			insertListTasks(t, st, []listTask{{pageID: "task1", name: "Write tests", project: tc.project}})

			got, _, err := st.ListTasks(t.Context(), NoLimit, false)
			if err != nil {
				t.Fatalf("ListTasks() want nil error, got %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("ListTasks() returned %d tasks, want 1: %v", len(got), got)
			}

			checkStringPtr(t, "ProjectPageID", got[0].ProjectPageID, tc.wantProject)
			checkStringPtr(t, "ProjectName", got[0].ProjectName, tc.wantName)
		})
	}
}

// A gone project mustn't drop its tasks from the list, or change what --limit counts
func TestListTasks_GoneProjectKeepsTasks(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
	insertListProjects(t, st, []listProject{
		{pageID: "kept", name: "Kept"},
		{pageID: "gone", name: "Gone", gone: true},
	})
	insertListTasks(t, st, []listTask{
		{pageID: "a", name: "In kept project", project: ptr("kept"), order: ptr(1.0)},
		{pageID: "b", name: "In gone project", project: ptr("gone"), order: ptr(2.0)},
		{pageID: "c", name: "No project", order: ptr(3.0)},
	})

	got, truncated, err := st.ListTasks(t.Context(), 2, false)
	if err != nil {
		t.Fatalf("ListTasks() want nil error, got %v", err)
	}

	if ids, want := listedPageIDs(got), []string{"a", "b"}; !slices.Equal(ids, want) {
		t.Errorf("page IDs = %v, want %v", ids, want)
	}
	if !truncated {
		t.Error("truncated = false, want true: a third task matched")
	}
}
