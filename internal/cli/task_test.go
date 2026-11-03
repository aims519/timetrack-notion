package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
