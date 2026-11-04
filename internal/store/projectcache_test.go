package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestRefreshProjects_WritesProject(t *testing.T) {
	tests := []struct {
		name    string
		project Project
	}{
		{
			name:    "named project",
			project: Project{ContextID: testContextID, PageID: "proj1", Name: "Sabbatical"},
		},
		// Edge cases
		{
			name:    "untitled project",
			project: Project{ContextID: testContextID, PageID: "proj1", Name: ""},
		},
		{
			name:    "name outside ASCII",
			project: Project{ContextID: testContextID, PageID: "proj1", Name: "Café ☕"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			gone, err := st.RefreshProjects(t.Context(), []Project{tc.project}, refreshNow)
			if err != nil {
				t.Fatalf("RefreshProjects() want nil error, got %v", err)
			}
			checkGoneCount(t, gone, ptr(0))

			rows := readCachedProjects(t, st)
			if len(rows) != 1 {
				t.Fatalf("got %d cached projects, want 1", len(rows))
			}
			got, ok := rows[tc.project.PageID]
			if !ok {
				t.Fatalf("no cached project with page_id %q", tc.project.PageID)
			}
			if got.name != tc.project.Name {
				t.Errorf("name = %q, want %q", got.name, tc.project.Name)
			}
			if got.goneAt != nil {
				t.Errorf("gone_at = %q, want NULL", *got.goneAt)
			}
		})
	}
}

func TestRefreshProjects_UpdatesExistingProject(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	_, err := st.connectionPool.ExecContext(t.Context(), `
		INSERT INTO projects(context_id, page_id, name)
		VALUES (?, 'proj1', 'Old name')`,
		testContextID)
	if err != nil {
		t.Fatalf("insert project failed: %v", err)
	}

	renamed := Project{ContextID: testContextID, PageID: "proj1", Name: "New name"}
	gone, err := st.RefreshProjects(t.Context(), []Project{renamed}, refreshNow)
	if err != nil {
		t.Fatalf("RefreshProjects() want nil error, got %v", err)
	}
	checkGoneCount(t, gone, ptr(0))

	rows := readCachedProjects(t, st)
	if len(rows) != 1 {
		t.Fatalf("got %d cached projects, want 1", len(rows))
	}
	if got := rows["proj1"].name; got != "New name" {
		t.Errorf("name = %q, want %q", got, "New name")
	}
	if got := rows["proj1"].goneAt; got != nil {
		t.Errorf("gone_at = %q, want NULL", *got)
	}
}

func TestRefreshProjects_GoneAt(t *testing.T) {
	type existing struct {
		pageID string
		goneAt *time.Time
	}

	tests := []struct {
		name     string
		existing []existing
		returned []string              // page IDs of the projects passed to RefreshProjects
		want     map[string]*time.Time // gone_at for each page ID; nil means not gone
		wantGone int
	}{
		{
			name:     "first refresh adds every project",
			returned: []string{"a", "b"},
			want:     map[string]*time.Time{"a": nil, "b": nil},
			wantGone: 0,
		},
		{
			name:     "missing projects marked gone",
			existing: []existing{{"a", nil}, {"b", nil}, {"c", nil}},
			returned: []string{"a"},
			want:     map[string]*time.Time{"a": nil, "b": &refreshNow, "c": &refreshNow},
			wantGone: 2,
		},
		{
			name:     "every returned project kept",
			existing: []existing{{"a", nil}, {"b", nil}, {"c", nil}},
			returned: []string{"a", "b", "c"},
			want:     map[string]*time.Time{"a": nil, "b": nil, "c": nil},
			wantGone: 0,
		},
		{
			name:     "returned project clears gone_at",
			existing: []existing{{"a", &refreshEarlier}},
			returned: []string{"a"},
			want:     map[string]*time.Time{"a": nil},
			wantGone: 0,
		},
		{
			name:     "new projects added and missing ones marked gone",
			existing: []existing{{"a", nil}},
			returned: []string{"b", "c"},
			want:     map[string]*time.Time{"a": &refreshNow, "b": nil, "c": nil},
			wantGone: 1,
		},
		// Edge cases
		{
			name:     "project already gone keeps its original gone_at",
			existing: []existing{{"a", nil}, {"b", &refreshEarlier}},
			returned: []string{"a"},
			want:     map[string]*time.Time{"a": nil, "b": &refreshEarlier},
			wantGone: 0,
		},
		{
			name:     "no projects marks every project gone",
			existing: []existing{{"a", nil}, {"b", nil}},
			returned: nil,
			want:     map[string]*time.Time{"a": &refreshNow, "b": &refreshNow},
			wantGone: 2,
		},
		{
			name:     "no projects and an empty cache",
			returned: nil,
			want:     map[string]*time.Time{},
			wantGone: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			for _, e := range tc.existing {
				insertCachedProject(t, st, testContextID, e.pageID, e.goneAt)
			}

			projects := make([]Project, len(tc.returned))
			for i, id := range tc.returned {
				projects[i] = Project{ContextID: testContextID, PageID: id, Name: "Returned project"}
			}

			gotGone, err := st.RefreshProjects(t.Context(), projects, refreshNow)
			if err != nil {
				t.Fatalf("RefreshProjects() want nil error, got %v", err)
			}

			// Only projects this refresh found gone are counted, not ones gone before it or ones that came back
			checkGoneCount(t, gotGone, &tc.wantGone)

			rows := readCachedProjects(t, st)
			if len(rows) != len(tc.want) {
				t.Errorf("got %d cached projects, want %d", len(rows), len(tc.want))
			}
			for id, wantGone := range tc.want {
				row, ok := rows[id]
				if !ok {
					t.Errorf("no cached project with page_id %q", id)
					continue
				}
				gotGone, err := FromNullableDBString(row.goneAt)
				if err != nil {
					t.Errorf("project %q: gone_at %q doesn't parse: %v", id, *row.goneAt, err)
					continue
				}
				checkTimePtr(t, "project "+id+" gone_at", gotGone, wantGone)
			}
		})
	}
}

func TestRefreshProjects_LeavesOtherContexts(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
	insertOtherContext(t, st)

	// Same page ID in both contexts, so a refresh that ignored context_id would mark it as returned
	insertCachedProject(t, st, otherContextID, "a", nil)
	insertCachedProject(t, st, otherContextID, "b", nil)

	gone, err := st.RefreshProjects(t.Context(), []Project{{ContextID: testContextID, PageID: "a", Name: "Returned project"}}, refreshNow)
	if err != nil {
		t.Fatalf("RefreshProjects() want nil error, got %v", err)
	}
	checkGoneCount(t, gone, ptr(0))

	var goneInOther int
	err = st.connectionPool.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM projects WHERE context_id = ? AND gone_at IS NOT NULL`,
		otherContextID).Scan(&goneInOther)
	if err != nil {
		t.Fatalf("count gone projects failed: %v", err)
	}
	if goneInOther != 0 {
		t.Errorf("got %d gone projects in the other context, want 0", goneInOther)
	}
}

func TestRefreshProjects_FailureChangesNothing(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	insertCachedProject(t, st, testContextID, "existing", nil)

	// The second project's context doesn't exist, so its insert breaks the foreign key
	// after every project has been marked gone and the first one written
	projects := []Project{
		{ContextID: testContextID, PageID: "good", Name: "Good project"},
		{ContextID: otherContextID, PageID: "bad", Name: "Bad project"},
	}

	gone, err := st.RefreshProjects(t.Context(), projects, refreshNow)
	if err == nil {
		t.Fatal("RefreshProjects() want error, got nil")
	}
	// Nothing was committed, so there's no count to report
	if gone != nil {
		t.Errorf("gone count = %d alongside an error, want nil", *gone)
	}
	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}

	rows := readCachedProjects(t, st)
	if len(rows) != 1 {
		t.Errorf("got %d cached projects, want 1 (only the existing one)", len(rows))
	}
	if row, ok := rows["existing"]; !ok {
		t.Error("existing project missing")
	} else if row.goneAt != nil {
		t.Errorf("existing project gone_at = %q, want NULL (refresh rolled back)", *row.goneAt)
	}

	got, err := st.CacheState(t.Context())
	if err != nil {
		t.Fatalf("CacheState() want nil error, got %v", err)
	}
	checkTimePtr(t, "ProjectsRefreshedAt", got.ProjectsRefreshedAt, nil)
}

func TestRefreshProjects_CacheState(t *testing.T) {
	tasksAt := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	failedAt := time.Date(2026, 11, 3, 8, 30, 0, 0, time.UTC)
	fullAt := time.Date(2026, 11, 2, 18, 30, 0, 0, time.UTC)
	plusOne := time.FixedZone("UTC+1", 60*60)

	tests := []struct {
		name          string
		priorProjects *time.Time
		projects      []Project
		now           time.Time
		wantProjects  time.Time
	}{
		{
			name:         "first refresh",
			projects:     []Project{{ContextID: testContextID, PageID: "a", Name: "Project"}},
			now:          refreshNow,
			wantProjects: refreshNow,
		},
		{
			name:          "later refresh replaces an earlier one",
			priorProjects: &refreshEarlier,
			projects:      []Project{{ContextID: testContextID, PageID: "a", Name: "Project"}},
			now:           refreshNow,
			wantProjects:  refreshNow,
		},
		// Edge cases
		{
			name:          "refresh with no projects is still recorded",
			priorProjects: &refreshEarlier,
			projects:      nil,
			now:           refreshNow,
			wantProjects:  refreshNow,
		},
		{
			name:         "time not in UTC is stored in UTC",
			projects:     []Project{{ContextID: testContextID, PageID: "a", Name: "Project"}},
			now:          time.Date(2026, 11, 3, 10, 15, 0, 0, plusOne),
			wantProjects: refreshNow,
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
				ToDBString(tasksAt), ToDBString(failedAt), ToDBString(fullAt), dbTimeOrNull(tc.priorProjects), testContextID)
			if err != nil {
				t.Fatalf("update cache_state failed: %v", err)
			}

			if _, err := st.RefreshProjects(t.Context(), tc.projects, tc.now); err != nil {
				t.Fatalf("RefreshProjects() want nil error, got %v", err)
			}

			// Reading back through CacheState also checks the time is stored in a format it can parse
			got, err := st.CacheState(t.Context())
			if err != nil {
				t.Fatalf("CacheState() after RefreshProjects() want nil error, got %v", err)
			}

			checkTimePtr(t, "ProjectsRefreshedAt", got.ProjectsRefreshedAt, &tc.wantProjects)

			// A projects refresh leaves the task timestamps alone, so the next task refresh
			// and its back-off still work from them
			checkTimePtr(t, "TasksRefreshedAt", got.TasksRefreshedAt, &tasksAt)
			checkTimePtr(t, "TasksRefreshFailedAt", got.TasksRefreshFailedAt, &failedAt)
			checkTimePtr(t, "TasksFullRefreshedAt", got.TasksFullRefreshedAt, &fullAt)
		})
	}
}

// cachedProject is a row of the projects table as stored
type cachedProject struct {
	name   string
	goneAt *string
}

// readCachedProjects returns every row of the projects table for the test context, by page ID
func readCachedProjects(t *testing.T, st *Store) map[string]cachedProject {
	t.Helper()

	rows, err := st.connectionPool.QueryContext(t.Context(), `
		SELECT page_id, name, gone_at
		FROM projects
		WHERE context_id = ?`, testContextID)
	if err != nil {
		t.Fatalf("query projects failed: %v", err)
	}
	defer func() { _ = rows.Close() }()

	result := map[string]cachedProject{}
	for rows.Next() {
		var id string
		var c cachedProject
		if err := rows.Scan(&id, &c.name, &c.goneAt); err != nil {
			t.Fatalf("scan project failed: %v", err)
		}
		result[id] = c
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading projects failed: %v", err)
	}

	return result
}

// insertCachedProject adds a row to the projects table, gone at goneAt if it isn't nil
func insertCachedProject(t *testing.T, st *Store, contextID, pageID string, goneAt *time.Time) {
	t.Helper()

	_, err := st.connectionPool.ExecContext(t.Context(), `
		INSERT INTO projects(context_id, page_id, name, gone_at)
		VALUES (?, ?, 'Existing project', ?)`,
		contextID, pageID, dbTimeOrNull(goneAt))
	if err != nil {
		t.Fatalf("insert project %q failed: %v", pageID, err)
	}
}

// checkGoneCount reports an error if got and want aren't both nil or the same count
func checkGoneCount(t *testing.T, got, want *int) {
	t.Helper()

	switch {
	case want == nil && got != nil:
		t.Errorf("gone count = %d, want nil", *got)
	case want != nil && got == nil:
		t.Errorf("gone count = nil, want %d", *want)
	case want != nil && *got != *want:
		t.Errorf("gone count = %d, want %d", *got, *want)
	}
}
