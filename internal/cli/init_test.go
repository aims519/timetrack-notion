package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
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

	return runInitOnDB(t, filepath.Join(t.TempDir(), "test.db"), baseURL, client, args...)
}

// runInitOnDB is runInit against the database at dbPath, for tests that need something stored first
func runInitOnDB(t *testing.T, dbPath, baseURL string, client *http.Client, args ...string) (int, []byte, []byte) {
	t.Helper()

	return runAgainstNotion(t, dbPath, baseURL, client, append([]string{"init"}, args...)...)
}

// runAgainstNotion runs tt with args and JSON output, using the database at dbPath and the Notion
// at baseURL. Use it rather than runTT for anything that could call Notion, because runTT uses the
// live URL
func runAgainstNotion(t *testing.T, dbPath, baseURL string, client *http.Client, args ...string) (int, []byte, []byte) {
	t.Helper()

	t.Setenv("TT_DB", dbPath)
	t.Setenv("TT_TIMEZONE", "UTC")
	setNotionToken(t, ptr(initTestToken))

	format := output.Table
	a := &app{format: &format, notionBaseURL: baseURL, notionHTTPClient: client}

	var stdout, stderr bytes.Buffer
	fullArgs := append(slices.Clone(args), "--output", "json")
	exit := run(t.Context(), a, fullArgs, &stdout, &stderr)

	return exit, stdout.Bytes(), stderr.Bytes()
}

// seedDataSources stores data source IDs in the default context. An empty ID stays unset
func seedDataSources(t *testing.T, dbPath, tasksID, projectsID string) {
	t.Helper()

	st, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", dbPath, err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	}()

	nc, err := st.NotionContext(t.Context(), store.DefaultContextID)
	if err != nil {
		t.Fatalf("NotionContext(): %v", err)
	}
	if tasksID != "" {
		nc.TasksDataSourceID = &tasksID
	}
	if projectsID != "" {
		nc.ProjectsDataSourceID = &projectsID
	}
	if err := st.SaveNotionContext(t.Context(), nc); err != nil {
		t.Fatalf("SaveNotionContext(): %v", err)
	}
}

// storedDataSources reads back the default context's data source IDs, empty if unset
func storedDataSources(t *testing.T, dbPath string) (string, string) {
	t.Helper()

	st, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("store.Open(%q): %v", dbPath, err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	}()

	nc, err := st.NotionContext(t.Context(), store.DefaultContextID)
	if err != nil {
		t.Fatalf("NotionContext(): %v", err)
	}

	var tasksID, projectsID string
	if nc.TasksDataSourceID != nil {
		tasksID = *nc.TasksDataSourceID
	}
	if nc.ProjectsDataSourceID != nil {
		projectsID = *nc.ProjectsDataSourceID
	}
	return tasksID, projectsID
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

// Each data source is the one looked up if its database was given, otherwise the stored one, and
// the result is what gets reported and saved (DESIGN.md §5)
func TestRun_Init_ResolvesDataSources(t *testing.T) {
	tasksDataSourcePath := "/data_sources/" + mapTasksDataSourceID

	tests := []struct {
		name         string
		args         []string
		storedTasks  string
		storedProj   string
		wantRequests []string // sorted paths
	}{
		{
			name:         "both_given_nothing_stored",
			args:         []string{"--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID},
			wantRequests: []string{tasksDataSourcePath, projectsDatabasePath, tasksDatabasePath},
		},
		{
			// A database given in this run replaces what's stored
			name:         "both_given_both_stored",
			args:         []string{"--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID},
			storedTasks:  "old-tasks-ds",
			storedProj:   "old-projects-ds",
			wantRequests: []string{tasksDataSourcePath, projectsDatabasePath, tasksDatabasePath},
		},
		{
			name:         "tasks_given_projects_stored",
			args:         []string{"--tasks-db", tasksDatabaseID},
			storedTasks:  "old-tasks-ds",
			storedProj:   mapProjectsDataSourceID,
			wantRequests: []string{tasksDataSourcePath, tasksDatabasePath},
		},
		{
			name:         "projects_given_tasks_stored",
			args:         []string{"--projects-db", projectsDatabaseID},
			storedTasks:  mapTasksDataSourceID,
			storedProj:   "old-projects-ds",
			wantRequests: []string{tasksDataSourcePath, projectsDatabasePath},
		},
		{
			name:         "none_given_both_stored",
			storedTasks:  mapTasksDataSourceID,
			storedProj:   mapProjectsDataSourceID,
			wantRequests: []string{tasksDataSourcePath},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, requested := fakeNotion(t, map[string]notionResponse{
				tasksDatabasePath:    databaseWithDataSource(mapTasksDataSourceID),
				projectsDatabasePath: databaseWithDataSource(mapProjectsDataSourceID),
				tasksDataSourcePath: tasksSchema([]string{
					titleProperty("Task"),
					relationProperty("Project", mapProjectsDataSourceID),
					orderProperty,
					doneProperty,
				}),
			})
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, tc.storedTasks, tc.storedProj)

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), tc.args...)
			if exit != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", exit, stderr)
			}

			if got := requested(); !slices.Equal(got, tc.wantRequests) {
				t.Errorf("paths requested = %v, want %v", got, tc.wantRequests)
			}

			var doc struct {
				Context struct {
					TasksDataSourceID    string `json:"tasks_data_source_id"`
					ProjectsDataSourceID string `json:"projects_data_source_id"`
				} `json:"context"`
			}
			if err := json.Unmarshal(stdout, &doc); err != nil {
				t.Fatalf("unable to decode document %q: %v", stdout, err)
			}
			if doc.Context.TasksDataSourceID != mapTasksDataSourceID {
				t.Errorf("output tasks data source = %q, want %q", doc.Context.TasksDataSourceID, mapTasksDataSourceID)
			}
			if doc.Context.ProjectsDataSourceID != mapProjectsDataSourceID {
				t.Errorf("output projects data source = %q, want %q", doc.Context.ProjectsDataSourceID, mapProjectsDataSourceID)
			}

			gotTasks, gotProjects := storedDataSources(t, dbPath)
			if gotTasks != mapTasksDataSourceID {
				t.Errorf("stored tasks data source = %q, want %q", gotTasks, mapTasksDataSourceID)
			}
			if gotProjects != mapProjectsDataSourceID {
				t.Errorf("stored projects data source = %q, want %q", gotProjects, mapProjectsDataSourceID)
			}
		})
	}
}

// Without a given or stored ID for both data sources, init stops before reading the tasks schema
// and saves nothing (DESIGN.md §5)
func TestRun_Init_DataSourceMissing_ReturnsNotInitialised(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		storedTasks  string
		storedProj   string
		wantRequests []string // sorted paths
	}{
		{
			name: "nothing_given_nothing_stored",
		},
		{
			name:         "tasks_given_nothing_stored",
			args:         []string{"--tasks-db", tasksDatabaseID},
			wantRequests: []string{tasksDatabasePath},
		},
		{
			name:         "projects_given_nothing_stored",
			args:         []string{"--projects-db", projectsDatabaseID},
			wantRequests: []string{projectsDatabasePath},
		},
		{
			name:        "only_tasks_stored",
			storedTasks: mapTasksDataSourceID,
		},
		{
			name:       "only_projects_stored",
			storedProj: mapProjectsDataSourceID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, requested := fakeNotion(t, map[string]notionResponse{
				tasksDatabasePath:    databaseWithDataSource(mapTasksDataSourceID),
				projectsDatabasePath: databaseWithDataSource(mapProjectsDataSourceID),
			})
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, tc.storedTasks, tc.storedProj)

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), tc.args...)

			checkInitError(t, exit, stdout, stderr, 5, clierr.NotInitialised)

			if got := requested(); !slices.Equal(got, tc.wantRequests) {
				t.Errorf("paths requested = %v, want %v", got, tc.wantRequests)
			}

			gotTasks, gotProjects := storedDataSources(t, dbPath)
			if gotTasks != tc.storedTasks {
				t.Errorf("stored tasks data source = %q, want %q", gotTasks, tc.storedTasks)
			}
			if gotProjects != tc.storedProj {
				t.Errorf("stored projects data source = %q, want %q", gotProjects, tc.storedProj)
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

const (
	mapTasksDataSourceID    = "tasks-ds"
	mapProjectsDataSourceID = "projects-ds"
	mapOtherDataSourceID    = "other-ds"

	// Property schemas as GET /v1/data_sources/{id} returns them. Each type's settings are an
	// object, unlike the values on a page: a checkbox's are {} rather than true or false
	orderProperty = `"Order": {"id": "ord", "name": "Order", "type": "number", "number": {"format": "number"}}`
	doneProperty  = `"Done": {"id": "don", "name": "Done", "type": "checkbox", "checkbox": {}}`
	notesProperty = `"Notes": {"id": "not", "name": "Notes", "type": "rich_text", "rich_text": {}}`
)

// Notion gives the title property the ID "title" whatever its name
func titleProperty(name string) string {
	return fmt.Sprintf(`%q: {"id": "title", "name": %q, "type": "title", "title": {}}`, name, name)
}

func relationProperty(name, targetDataSourceID string) string {
	return fmt.Sprintf(`%q: {"id": "rel", "name": %q, "type": "relation", "relation": {"database_id": "target-db", "data_source_id": %q, "type": "dual_property", "dual_property": {"synced_property_name": "Tasks", "synced_property_id": "syn"}}}`, name, name, targetDataSourceID)
}

// databaseWithDataSource is a database lookup response listing one data source
func databaseWithDataSource(dataSourceID string) notionResponse {
	return notionResponse{http.StatusOK, fmt.Sprintf(`{"object": "database", "data_sources": [{"id": %q, "name": "Data source"}]}`, dataSourceID)}
}

// tasksSchema is the response for the tasks data source, with the given property schemas
func tasksSchema(properties []string) notionResponse {
	return notionResponse{http.StatusOK, fmt.Sprintf(`{"object": "data_source", "id": %q, "properties": {%s}}`, mapTasksDataSourceID, strings.Join(properties, ", "))}
}

// mapPropertiesAgainst runs mapTaskProperties against a fake Notion whose tasks data source has the
// given property schemas
func mapPropertiesAgainst(t *testing.T, properties []string) (map[string]string, []output.Warning, error) {
	t.Helper()

	s, _ := fakeNotion(t, map[string]notionResponse{
		"/data_sources/" + mapTasksDataSourceID: tasksSchema(properties),
	})

	client := notion.NewClient(s.URL, initTestToken, s.Client())
	return mapTaskProperties(t.Context(), client, mapTasksDataSourceID, mapProjectsDataSourceID)
}

// checkPropertyNotFound fails the test unless mapping failed with not_found, naming the tasks data
// source (DESIGN.md §8)
func checkPropertyNotFound(t *testing.T, got map[string]string, err error) {
	t.Helper()

	var cliErr *clierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("mapTaskProperties() = %v, %v, want a *clierr.Error", got, err)
	}
	if cliErr.Code != clierr.NotFound {
		t.Errorf("error code = %q, want %q (message: %q)", cliErr.Code, clierr.NotFound, cliErr.Message)
	}
	if id := cliErr.Details["data_source_id"]; id != mapTasksDataSourceID {
		t.Errorf("details data_source_id = %v, want %q", id, mapTasksDataSourceID)
	}
	if got != nil {
		t.Errorf("properties = %v, want nil", got)
	}
}

func TestMapTaskProperties_FindsProject(t *testing.T) {
	tests := []struct {
		name        string
		properties  []string
		wantProject string
	}{
		{
			name: "default_names",
			properties: []string{
				titleProperty("Task"),
				relationProperty("Project", mapProjectsDataSourceID),
				orderProperty,
				doneProperty,
			},
			wantProject: "Project",
		},
		{
			name: "relation_found_by_target_not_name",
			properties: []string{
				titleProperty("Task"),
				relationProperty("Client work", mapProjectsDataSourceID),
				notesProperty,
				doneProperty,
			},
			wantProject: "Client work",
		},
		{
			name: "relation_to_another_data_source_ignored",
			properties: []string{
				titleProperty("Task"),
				relationProperty("Blocked by", mapOtherDataSourceID),
				relationProperty("Client work", mapProjectsDataSourceID),
				relationProperty("Owner", mapOtherDataSourceID),
				doneProperty,
			},
			wantProject: "Client work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Warnings are covered by TestMapTaskProperties_OrderProperty
			got, _, err := mapPropertiesAgainst(t, tt.properties)
			if err != nil {
				t.Fatalf("mapTaskProperties() error = %v", err)
			}

			// Checked key by key because order and done have their own tests
			if got["project"] != tt.wantProject {
				t.Errorf("project = %q, want %q", got["project"], tt.wantProject)
			}
			// Task names are read from a page's title by type, so aren't mapped (DESIGN.md §4)
			if name, ok := got["task"]; ok {
				t.Errorf("task = %q, want no key", name)
			}
		})
	}
}

func TestMapTaskProperties_PropertyNotFound(t *testing.T) {
	tests := []struct {
		name       string
		properties []string
	}{
		{
			name: "no_relations",
			properties: []string{
				titleProperty("Task"),
				notesProperty,
				doneProperty,
			},
		},
		{
			name: "relations_only_to_other_data_sources",
			properties: []string{
				titleProperty("Task"),
				relationProperty("Project", mapOtherDataSourceID),
				doneProperty,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := mapPropertiesAgainst(t, tt.properties)
			checkPropertyNotFound(t, got, err)
		})
	}
}

// Order is optional: without a number property of that name, mapping still succeeds and warns
func TestMapTaskProperties_OrderProperty(t *testing.T) {
	required := []string{
		titleProperty("Task"),
		relationProperty("Project", mapProjectsDataSourceID),
		doneProperty,
	}

	tests := []struct {
		name         string
		properties   []string
		wantOrder    string
		wantWarnings []output.WarningCode
	}{
		{
			name:       "found",
			properties: []string{orderProperty, notesProperty},
			wantOrder:  "Order",
		},
		{
			// The map keeps Notion's spelling, which is what a page's properties are keyed by
			name:       "name_matched_ignoring_case",
			properties: []string{`"ORDER": {"id": "ord", "name": "ORDER", "type": "number", "number": {"format": "number"}}`},
			wantOrder:  "ORDER",
		},
		{
			name:         "missing",
			properties:   []string{notesProperty},
			wantWarnings: []output.WarningCode{output.OrderPropertyMissing},
		},
		{
			name:         "other_number_property_not_used",
			properties:   []string{`"Estimate": {"id": "est", "name": "Estimate", "type": "number", "number": {"format": "number"}}`},
			wantWarnings: []output.WarningCode{output.OrderPropertyMissing},
		},
		{
			name:         "right_name_wrong_type",
			properties:   []string{`"Order": {"id": "ord", "name": "Order", "type": "rich_text", "rich_text": {}}`},
			wantWarnings: []output.WarningCode{output.OrderPropertyMissing},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warnings, err := mapPropertiesAgainst(t, append(slices.Clone(required), tt.properties...))
			if err != nil {
				t.Fatalf("mapTaskProperties() error = %v", err)
			}

			order, ok := got["order"]
			if order != tt.wantOrder {
				t.Errorf("order = %q, want %q", order, tt.wantOrder)
			}
			if tt.wantOrder == "" && ok {
				t.Errorf("order key present with value %q, want no key", order)
			}

			var codes []output.WarningCode
			for _, w := range warnings {
				codes = append(codes, w.Code)
			}
			if !slices.Equal(codes, tt.wantWarnings) {
				t.Errorf("warning codes = %v, want %v", codes, tt.wantWarnings)
			}
		})
	}
}

// Done is required: without it every finished task would look open, so mapping fails
func TestMapTaskProperties_DoneProperty(t *testing.T) {
	required := []string{
		titleProperty("Task"),
		relationProperty("Project", mapProjectsDataSourceID),
		orderProperty,
	}

	tests := []struct {
		name       string
		properties []string
		wantDone   string // Empty means mapping should fail
	}{
		{
			name:       "found",
			properties: []string{doneProperty, notesProperty},
			wantDone:   "Done",
		},
		{
			name:       "name_matched_ignoring_case",
			properties: []string{`"DONE": {"id": "don", "name": "DONE", "type": "checkbox", "checkbox": {}}`},
			wantDone:   "DONE",
		},
		{
			name:       "missing",
			properties: []string{notesProperty},
		},
		{
			name:       "other_checkbox_not_used",
			properties: []string{`"Archived": {"id": "arc", "name": "Archived", "type": "checkbox", "checkbox": {}}`},
		},
		{
			// Notion's status type is a common way to track completion, but isn't supported
			name:       "right_name_wrong_type",
			properties: []string{`"Done": {"id": "don", "name": "Done", "type": "status", "status": {"options": [], "groups": []}}`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := mapPropertiesAgainst(t, append(slices.Clone(required), tt.properties...))

			if tt.wantDone == "" {
				checkPropertyNotFound(t, got, err)
				return
			}

			if err != nil {
				t.Fatalf("mapTaskProperties() error = %v", err)
			}
			if got["done"] != tt.wantDone {
				t.Errorf("done = %q, want %q", got["done"], tt.wantDone)
			}
		})
	}
}
