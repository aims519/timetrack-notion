package cli

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/gofrs/flock"
)

const (
	entriesQueryPath = "/data_sources/" + entriesDataSourceID + "/query"
	pagesPath        = "/pages"
)

func TestSyncLockFilePath(t *testing.T) {
	tests := []struct {
		name   string
		dbPath string
		want   string
	}{
		{name: "default_name", dbPath: "/home/u/.local/share/tt/tt.db", want: "/home/u/.local/share/tt/tt.db.lock"},
		{name: "custom_name", dbPath: "/tmp/dev/work.db", want: "/tmp/dev/work.db.lock"},
		{name: "no_extension", dbPath: "/tmp/dev/tt", want: "/tmp/dev/tt.lock"},
		{name: "relative", dbPath: "dev/tt.db", want: "dev/tt.db.lock"},
		{name: "bare_file", dbPath: "tt.db", want: "tt.db.lock"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.FromSlash(tc.dbPath)
			want := filepath.FromSlash(tc.want)

			got := syncLockFilePath(dbPath)
			if got != want {
				t.Errorf("syncLockFilePath(%q) = %q, want %q", dbPath, got, want)
			}
		})
	}
}

// checkErrorDocument fails the test unless a failed command wrote nothing to stdout and an
// error document with the wanted code to stderr
func checkErrorDocument(t *testing.T, stdout, stderr []byte, wantCode clierr.ErrorCode) {
	t.Helper()

	if len(stdout) != 0 {
		t.Errorf("wrote to stdout on failure: %q", stdout)
	}

	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr, &doc); err != nil {
		t.Fatalf("unable to decode error document %q: %v", stderr, err)
	}
	if doc.Error.Code != string(wantCode) {
		t.Errorf("error code want %q, got %q", wantCode, doc.Error.Code)
	}
}

func TestRun_Sync_Errors(t *testing.T) {
	tests := []struct {
		name      string
		connected bool
		held      bool // another process holds the sync lock
		wantExit  int
		wantCode  clierr.ErrorCode
	}{
		{name: "lock_free", connected: true, wantExit: 0},
		{name: "lock_held", connected: true, held: true, wantExit: 4, wantCode: clierr.SyncBusy},
		{name: "not_initialised", connected: false, wantExit: 5, wantCode: clierr.NotInitialised},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			if tc.connected {
				seedConnectedContext(t, dbPath)
			}

			if tc.held {
				// Built by hand rather than with syncLockFilePath, so the test
				// also checks the lock sits beside the TT_DB file (§6.5)
				holder := flock.New(dbPath + ".lock")
				locked, err := holder.TryLock()
				if err != nil || !locked {
					t.Fatalf("unable to hold lock for test: locked=%v, err=%v", locked, err)
				}
				t.Cleanup(func() { _ = holder.Unlock() })
			}

			// No responses: there's nothing to sync, so any request fails the test
			s, requested := fakeNotion(t, map[string]notionResponse{})

			code, stdout, stderr := runAgainstNotion(t, dbPath, s.URL, s.Client(), "sync")
			if code != tc.wantExit {
				t.Fatalf("sync = %d, want %d (stderr: %q)", code, tc.wantExit, stderr)
			}
			if paths := requested(); len(paths) != 0 {
				t.Errorf("sync requested %v, want no requests", paths)
			}

			if tc.wantExit != 0 {
				checkErrorDocument(t, stdout, stderr, tc.wantCode)
			}
		})
	}
}

// The token comes from NOTION_TOKEN only, and an empty one counts as unset (§9)
func TestRun_Sync_NoToken(t *testing.T) {
	tests := []struct {
		name  string
		token *string
	}{
		{name: "unset", token: nil},
		{name: "empty", token: ptr("")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedConnectedContext(t, dbPath)
			t.Setenv("TT_DB", dbPath)
			setNotionToken(t, tc.token)

			// runTT uses the live Notion URL, which is safe only because no token stops the
			// command before any request
			code, stdout, stderr := runTT(t, "sync")
			if code != 5 {
				t.Fatalf("sync = %d, want 5 (stderr: %q)", code, stderr)
			}
			checkErrorDocument(t, stdout, stderr, clierr.NoToken)
		})
	}
}

// A stopped entry with a task is created in Notion and no longer counts as pending
func TestRun_Sync_CreatesEntryPage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	seedAttachTask(t, dbPath)

	s, requested := fakeNotion(t, map[string]notionResponse{
		entriesDataSourcePath: entriesLinkedTo("tasks-ds"),
		entriesQueryPath:      {http.StatusOK, `{"object": "list", "results": [], "has_more": false, "next_cursor": null}`},
		pagesPath:             {http.StatusOK, `{"object": "page", "id": "entry-page-1"}`},
	})

	steps := [][]string{
		{"start", "writing tests", "--task", attachTaskID, "--at", "2026-01-01T09:00:00Z"},
		{"stop", "--at", "2026-01-01T10:00:00Z"},
	}
	for _, args := range steps {
		code, _, stderr := runAgainstNotion(t, dbPath, s.URL, s.Client(), args...)
		if code != 0 {
			t.Fatalf("%s = %d, want 0 (stderr: %q)", args[0], code, stderr)
		}
	}
	if got := runStatus(t).Counts.Pending; got != 1 {
		t.Fatalf("pending before sync = %d, want 1", got)
	}

	code, _, stderr := runAgainstNotion(t, dbPath, s.URL, s.Client(), "sync")
	if code != 0 {
		t.Fatalf("sync = %d, want 0 (stderr: %q)", code, stderr)
	}

	// The data source is checked, then the entry is looked up by Local ID and created (§6.2, §6.3)
	if got, want := requested(), []string{entriesDataSourcePath, entriesQueryPath, pagesPath}; !slices.Equal(got, want) {
		t.Errorf("sync requested %v, want %v", got, want)
	}
	if got := runStatus(t).Counts.Pending; got != 0 {
		t.Errorf("pending after sync = %d, want 0", got)
	}
}

// A rejected token, a missing entries data source or a rate limit stops the run as a failed
// command, and the entry stays pending for the next run (§6.3, §8)
func TestRun_Sync_AbortsRun(t *testing.T) {
	var (
		dataSourceOK  = entriesLinkedTo("tasks-ds")
		unauthorized  = notionResponse{http.StatusUnauthorized, `{"object": "error", "status": 401, "code": "unauthorized", "message": "API token is invalid."}`}
		rateLimited   = notionResponse{http.StatusTooManyRequests, `{"object": "error", "status": 429, "code": "rate_limited", "message": "Rate limited"}`}
		notFoundError = notionResponse{http.StatusNotFound, `{"object": "error", "status": 404, "code": "object_not_found", "message": "Could not find data source."}`}
	)

	tests := []struct {
		name          string
		responses     map[string]notionResponse
		wantRequested []string
		wantExit      int
		wantCode      clierr.ErrorCode
	}{
		{
			// A bad token fails the data source check, before any entry
			name:          "access_denied",
			responses:     map[string]notionResponse{entriesDataSourcePath: unauthorized},
			wantRequested: []string{entriesDataSourcePath},
			wantExit:      5,
			wantCode:      clierr.AccessDenied,
		},
		{
			// Un-shared or deleted before the run (§6.3)
			name:          "data_source_not_found",
			responses:     map[string]notionResponse{entriesDataSourcePath: notFoundError},
			wantRequested: []string{entriesDataSourcePath},
			wantExit:      3,
			wantCode:      clierr.NotFound,
		},
		{
			// Trashed, which Notion still answers with a 200 (decision 25)
			name:          "data_source_in_trash",
			responses:     map[string]notionResponse{entriesDataSourcePath: entriesSchema(true, titleProperty("Title"), relationProperty("Task", "tasks-ds"))},
			wantRequested: []string{entriesDataSourcePath},
			wantExit:      3,
			wantCode:      clierr.NotFound,
		},
		{
			// Un-shared after the check, so the lookup is the first to see it
			name:          "data_source_gone_during_run",
			responses:     map[string]notionResponse{entriesDataSourcePath: dataSourceOK, entriesQueryPath: notFoundError},
			wantRequested: []string{entriesDataSourcePath, entriesQueryPath},
			wantExit:      3,
			wantCode:      clierr.NotFound,
		},
		{
			name:          "rate_limited",
			responses:     map[string]notionResponse{entriesDataSourcePath: dataSourceOK, entriesQueryPath: rateLimited},
			wantRequested: []string{entriesDataSourcePath, entriesQueryPath},
			wantExit:      6,
			wantCode:      clierr.Offline,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedAttachTask(t, dbPath)

			s, requested := fakeNotion(t, tc.responses)

			steps := [][]string{
				{"start", "writing tests", "--task", attachTaskID, "--at", "2026-01-01T09:00:00Z"},
				{"stop", "--at", "2026-01-01T10:00:00Z"},
			}
			for _, args := range steps {
				code, _, stderr := runAgainstNotion(t, dbPath, s.URL, s.Client(), args...)
				if code != 0 {
					t.Fatalf("%s = %d, want 0 (stderr: %q)", args[0], code, stderr)
				}
			}

			code, stdout, stderr := runAgainstNotion(t, dbPath, s.URL, s.Client(), "sync")
			if code != tc.wantExit {
				t.Fatalf("sync = %d, want %d (stderr: %q)", code, tc.wantExit, stderr)
			}
			checkErrorDocument(t, stdout, stderr, tc.wantCode)

			if got := requested(); !slices.Equal(got, tc.wantRequested) {
				t.Errorf("sync requested %v, want %v", got, tc.wantRequested)
			}
			if got := runStatus(t).Counts.Pending; got != 1 {
				t.Errorf("pending after sync = %d, want 1", got)
			}
		})
	}
}
