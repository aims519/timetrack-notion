package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
)

const (
	initTestToken = "secret-test-token"

	tasksDatabaseID    = "tasks-db"
	projectsDatabaseID = "projects-db"
	tasksDatabasePath  = "/databases/" + tasksDatabaseID
	// Sorts before tasksDatabasePath, which is the order fakeNotion reports requests in
	projectsDatabasePath = "/databases/" + projectsDatabaseID
)

type notionResponse struct {
	status int
	body   string
}

var databaseFound = notionResponse{http.StatusOK, `{"object": "database", "data_sources": [{"id": "ds-1", "name": "Data source"}]}`}

// fakeNotion answers each path from responses, and checks every request carries the token from
// NOTION_TOKEN. The returned function gives the paths requested, sorted, so a test doesn't depend
// on which database is looked up first
func fakeNotion(t *testing.T, responses map[string]notionResponse) (*httptest.Server, func() []string) {
	t.Helper()

	// The handler runs on the server's goroutine, so the paths need a lock
	var mu sync.Mutex
	var paths []string

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()

		if got, want := r.Header.Get("Authorization"), "Bearer "+initTestToken; got != want {
			t.Errorf("Authorization header = %q, want %q", got, want)
		}

		res, ok := responses[r.URL.Path]
		if !ok {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.WriteHeader(res.status)
		if _, err := w.Write([]byte(res.body)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	requested := func() []string {
		mu.Lock()
		defer mu.Unlock()

		sorted := slices.Clone(paths)
		slices.Sort(sorted)
		return sorted
	}

	return s, requested
}

// runInit runs tt init with args and JSON output against the Notion at baseURL, returning the exit
// code, stdout and stderr. It goes through run rather than Run, because Run uses the live Notion URL
func runInit(t *testing.T, baseURL string, client *http.Client, args ...string) (int, []byte, []byte) {
	t.Helper()

	t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
	t.Setenv("TT_TIMEZONE", "UTC")
	setNotionToken(t, ptr(initTestToken))

	format := output.Table
	a := &app{format: &format, notionBaseURL: baseURL, notionHTTPClient: client}

	var stdout, stderr bytes.Buffer
	fullArgs := append(append([]string{"init"}, args...), "--output", "json")
	exit := run(t.Context(), a, fullArgs, &stdout, &stderr)

	return exit, stdout.Bytes(), stderr.Bytes()
}

// checkInitError fails the test unless the command failed with the given exit code and error code,
// and wrote nothing to stdout
func checkInitError(t *testing.T, exit int, stdout, stderr []byte, wantExit int, wantCode clierr.ErrorCode) {
	t.Helper()

	if exit != wantExit {
		t.Errorf("exit code = %d, want %d (stderr: %q)", exit, wantExit, stderr)
	}

	// A failed command must write nothing to stdout
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
		t.Errorf("error code = %q, want %q", doc.Error.Code, wantCode)
	}
}

// The code from the failed lookup reaches the user, along with its exit code (DESIGN.md §8)
func TestRun_Init_NotionErrors(t *testing.T) {
	unauthorized := notionResponse{http.StatusUnauthorized, `{"object": "error", "status": 401, "code": "unauthorized", "message": "API token is invalid."}`}
	notFound := notionResponse{http.StatusNotFound, `{"object": "error", "status": 404, "code": "object_not_found", "message": "Could not find database."}`}
	badRequest := notionResponse{http.StatusBadRequest, `{"object": "error", "status": 400, "code": "validation_error", "message": "path failed validation"}`}
	serverError := notionResponse{http.StatusInternalServerError, `{"object": "error", "status": 500, "code": "internal_server_error", "message": "Unexpected error occurred."}`}
	noDataSources := notionResponse{http.StatusOK, `{"object": "database", "data_sources": []}`}

	tests := []struct {
		name      string
		args      []string
		responses map[string]notionResponse
		wantExit  int
		wantCode  clierr.ErrorCode
	}{
		{
			name:      "invalid_token",
			args:      []string{"--tasks-db", tasksDatabaseID},
			responses: map[string]notionResponse{tasksDatabasePath: unauthorized},
			wantExit:  5,
			wantCode:  clierr.AccessDenied,
		},
		{
			name:      "tasks_database_not_found",
			args:      []string{"--tasks-db", tasksDatabaseID},
			responses: map[string]notionResponse{tasksDatabasePath: notFound},
			wantExit:  3,
			wantCode:  clierr.NotFound,
		},
		{
			name:      "projects_database_not_found",
			args:      []string{"--projects-db", projectsDatabaseID},
			responses: map[string]notionResponse{projectsDatabasePath: notFound},
			wantExit:  3,
			wantCode:  clierr.NotFound,
		},
		{
			name:      "bad_request",
			args:      []string{"--tasks-db", tasksDatabaseID},
			responses: map[string]notionResponse{tasksDatabasePath: badRequest},
			wantExit:  2,
			wantCode:  clierr.Usage,
		},
		{
			name:      "server_error",
			args:      []string{"--tasks-db", tasksDatabaseID},
			responses: map[string]notionResponse{tasksDatabasePath: serverError},
			wantExit:  10,
			wantCode:  clierr.Internal,
		},
		{
			name:      "no_data_sources",
			args:      []string{"--tasks-db", tasksDatabaseID},
			responses: map[string]notionResponse{tasksDatabasePath: noDataSources},
			wantExit:  3,
			wantCode:  clierr.NotFound,
		},
		// Edge cases
		{
			// One database being fine doesn't hide the other one failing
			name: "tasks_found_projects_not_found",
			args: []string{"--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID},
			responses: map[string]notionResponse{
				tasksDatabasePath:    databaseFound,
				projectsDatabasePath: notFound,
			},
			wantExit: 3,
			wantCode: clierr.NotFound,
		},
		{
			name: "projects_found_tasks_not_found",
			args: []string{"--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID},
			responses: map[string]notionResponse{
				tasksDatabasePath:    notFound,
				projectsDatabasePath: databaseFound,
			},
			wantExit: 3,
			wantCode: clierr.NotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := fakeNotion(t, tc.responses)

			exit, stdout, stderr := runInit(t, s.URL, s.Client(), tc.args...)

			checkInitError(t, exit, stdout, stderr, tc.wantExit, tc.wantCode)
		})
	}
}

// The selector says which database has several data sources, and the candidates reach the user
// through the error document (DESIGN.md §8 and decision 23)
func TestRun_Init_SeveralDataSources_ReturnsCandidates(t *testing.T) {
	type candidate struct {
		DataSourceID string `json:"data_source_id"`
		Name         string `json:"name"`
	}
	wantCandidates := []candidate{
		{DataSourceID: "ds-1", Name: "Active"},
		{DataSourceID: "ds-2", Name: "Archive"},
	}

	severalDataSources := notionResponse{http.StatusOK, `{
		"object": "database",
		"data_sources": [
			{"id": "ds-1", "name": "Active"},
			{"id": "ds-2", "name": "Archive"}
		]
	}`}

	tests := []struct {
		name         string
		args         []string
		responses    map[string]notionResponse
		wantSelector string
	}{
		{
			name:         "tasks_database",
			args:         []string{"--tasks-db", tasksDatabaseID},
			responses:    map[string]notionResponse{tasksDatabasePath: severalDataSources},
			wantSelector: tasksDatabaseID,
		},
		{
			name:         "projects_database",
			args:         []string{"--projects-db", projectsDatabaseID},
			responses:    map[string]notionResponse{projectsDatabasePath: severalDataSources},
			wantSelector: projectsDatabaseID,
		},
		// Edge cases
		{
			// The selector names the database with the problem, not the one that was fine
			name: "projects_database_with_tasks_database_fine",
			args: []string{"--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID},
			responses: map[string]notionResponse{
				tasksDatabasePath:    databaseFound,
				projectsDatabasePath: severalDataSources,
			},
			wantSelector: projectsDatabaseID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := fakeNotion(t, tc.responses)

			exit, stdout, stderr := runInit(t, s.URL, s.Client(), tc.args...)

			checkInitError(t, exit, stdout, stderr, 3, clierr.AmbiguousSelector)

			var doc struct {
				Error struct {
					Details struct {
						Selector   string      `json:"selector"`
						Candidates []candidate `json:"candidates"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr, err)
			}

			if doc.Error.Details.Selector != tc.wantSelector {
				t.Errorf("details.selector = %q, want %q", doc.Error.Details.Selector, tc.wantSelector)
			}
			if !slices.Equal(doc.Error.Details.Candidates, wantCandidates) {
				t.Errorf("details.candidates = %+v, want %+v (stderr: %s)", doc.Error.Details.Candidates, wantCandidates, stderr)
			}
		})
	}
}

// When Notion can't be reached there's no HTTP status to map, so the error is offline (DESIGN.md §8)
func TestRun_Init_NotionUnreachable_ReturnsOffline(t *testing.T) {
	// NewTestServer's in-memory network accepts every connection, even once the server is closed,
	// so this needs NewServer's real loopback listener to get a refusal
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to a closed server: %s %s", r.Method, r.URL.Path)
	}))
	url, client := s.URL, s.Client()

	// Closing it straight away leaves a URL that refuses connections
	s.Close()

	exit, stdout, stderr := runInit(t, url, client, "--tasks-db", tasksDatabaseID)

	checkInitError(t, exit, stdout, stderr, 6, clierr.Offline)

	// This is what the user sees, so it must never carry the token (DESIGN.md §9)
	if bytes.Contains(stderr, []byte(initTestToken)) {
		t.Errorf("error includes the token: %q", stderr)
	}
}

// Each flag looks up its own database, and neither needs the other
func TestRun_Init_LooksUpGivenDatabases(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string // sorted paths
	}{
		{
			name: "tasks_only",
			args: []string{"--tasks-db", tasksDatabaseID},
			want: []string{tasksDatabasePath},
		},
		{
			name: "projects_only",
			args: []string{"--projects-db", projectsDatabaseID},
			want: []string{projectsDatabasePath},
		},
		{
			name: "both",
			args: []string{"--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID},
			want: []string{projectsDatabasePath, tasksDatabasePath},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, requested := fakeNotion(t, map[string]notionResponse{
				tasksDatabasePath:    databaseFound,
				projectsDatabasePath: databaseFound,
			})

			exit, _, stderr := runInit(t, s.URL, s.Client(), tc.args...)
			if exit != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", exit, stderr)
			}

			if got := requested(); !slices.Equal(got, tc.want) {
				t.Errorf("paths requested = %v, want %v", got, tc.want)
			}
		})
	}
}

// A usage error is local and immediate (DESIGN.md §7), so neither flag is looked up if either is
// empty, and a mistyped command doesn't report offline when Notion can't be reached
func TestRun_Init_EmptyFlag_MakesNoRequests(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "tasks_empty_projects_set", args: []string{"--tasks-db", "", "--projects-db", projectsDatabaseID}},
		{name: "tasks_set_projects_empty", args: []string{"--tasks-db", tasksDatabaseID, "--projects-db", ""}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, requested := fakeNotion(t, map[string]notionResponse{
				tasksDatabasePath:    databaseFound,
				projectsDatabasePath: databaseFound,
			})

			exit, stdout, stderr := runInit(t, s.URL, s.Client(), tc.args...)

			checkInitError(t, exit, stdout, stderr, 2, clierr.Usage)

			if got := requested(); len(got) != 0 {
				t.Errorf("paths requested = %v, want none", got)
			}
		})
	}
}

// These cases all fail before any Notion request is made, so they need no fake server.
// runTT goes through Run, which uses the live Notion URL, so a case that gets as far as a
// lookup doesn't belong in this table
func TestRun_Init_Errors(t *testing.T) {
	tests := []struct {
		name        string
		token       *string // nil leaves NOTION_TOKEN unset
		timezone    *string // nil means UTC
		args        []string
		wantExit    int
		wantCode    clierr.ErrorCode
		wantMessage string // part of the error message; empty skips the check
	}{
		{name: "token_unset", args: []string{"init"}, wantExit: 5, wantCode: clierr.NoToken},
		{name: "token_empty", token: ptr(""), args: []string{"init"}, wantExit: 5, wantCode: clierr.NoToken},
		{
			name:     "token_unset_with_database_ids",
			args:     []string{"init", "--tasks-db", "db-1", "--projects-db", "db-2"},
			wantExit: 5,
			wantCode: clierr.NoToken,
		},
		// The token is never accepted as a flag (DESIGN.md §9). runTT adds --output json at the
		// end, but flags after a bad one aren't parsed, so it comes first here to get the error as JSON
		{
			name:     "token_flag_rejected",
			token:    ptr("test-token"),
			args:     []string{"--output", "json", "init", "--token", "secret"},
			wantExit: 2,
			wantCode: clierr.Usage,
		},
		{
			name:        "tasks_db_empty",
			token:       ptr("test-token"),
			args:        []string{"init", "--tasks-db", ""},
			wantExit:    2,
			wantCode:    clierr.Usage,
			wantMessage: "--tasks-db",
		},
		{
			name:        "projects_db_empty",
			token:       ptr("test-token"),
			args:        []string{"init", "--projects-db", ""},
			wantExit:    2,
			wantCode:    clierr.Usage,
			wantMessage: "--projects-db",
		},
		{
			name:     "timezone_empty",
			token:    ptr("test-token"),
			timezone: ptr(""),
			args:     []string{"init"},
			wantExit: 2,
			wantCode: clierr.Usage,
		},
		{
			name:     "timezone_unknown",
			token:    ptr("test-token"),
			timezone: ptr("Not/AZone"),
			args:     []string{"init"},
			wantExit: 2,
			wantCode: clierr.Usage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
			setNotionToken(t, tc.token)

			timezone := "UTC"
			if tc.timezone != nil {
				timezone = *tc.timezone
			}
			t.Setenv("TT_TIMEZONE", timezone)

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
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr, err)
			}
			if doc.Error.Code != string(tc.wantCode) {
				t.Errorf("error code = %q, want %q", doc.Error.Code, tc.wantCode)
			}
			if !strings.Contains(doc.Error.Message, tc.wantMessage) {
				t.Errorf("error message = %q, want it to include %q", doc.Error.Message, tc.wantMessage)
			}
		})
	}
}
