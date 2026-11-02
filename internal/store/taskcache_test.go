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
