package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

	initParentPageID = "parent-page"
	// Where init creates the Time entries database. fakeNotion doesn't look at the method, so a
	// request here counts as a create. Sorts after the data sources and before the lookups
	createDatabasePath = "/databases"

	entriesDataSourceID   = "entries-ds"
	entriesDataSourcePath = "/data_sources/" + entriesDataSourceID

	// Where init checks the token. Sorts after every other path, so it's last in fakeNotion's report
	usersMePath = "/users/me"
)

type notionResponse struct {
	status int
	body   string
}

var (
	databaseFound   = notionResponse{http.StatusOK, `{"object": "database", "data_sources": [{"id": "ds-1", "name": "Data source"}]}`}
	databaseCreated = notionResponse{http.StatusOK, fmt.Sprintf(`{"object": "database", "id": "entries-db", "data_sources": [{"id": %q, "name": "Time entries"}]}`, entriesDataSourceID)}
	tokenValid      = notionResponse{http.StatusOK, `{"object": "user", "id": "bot-1", "name": "tt", "type": "bot", "bot": {}}`}
)

// withParentPage is args plus --parent-page, for tests where init gets as far as needing it but
// aren't about the Time entries database
func withParentPage(args ...string) []string {
	return append(slices.Clone(args), "--parent-page", initParentPageID)
}

// requestCount is how many times path appears in paths
func requestCount(paths []string, path string) int {
	count := 0
	for _, p := range paths {
		if p == path {
			count++
		}
	}
	return count
}

// fakeNotion answers each path from responses, and checks every request carries the token from
// NOTION_TOKEN. The token check succeeds unless responses says otherwise, because init makes it on
// every run. The returned function gives the paths requested, including the token check, sorted so
// a test doesn't depend on which database is looked up first
func fakeNotion(t *testing.T, responses map[string]notionResponse) (*httptest.Server, func() []string) {
	t.Helper()

	sequences := make(map[string][]notionResponse, len(responses))
	for path, res := range responses {
		sequences[path] = []notionResponse{res}
	}
	return fakeNotionSequence(t, sequences)
}

// fakeNotionSequence is fakeNotion for a path that answers differently each time: each request
// gets the next response for its path, and the last one repeats once they run out
func fakeNotionSequence(t *testing.T, responses map[string][]notionResponse) (*httptest.Server, func() []string) {
	t.Helper()

	// The handler runs on the server's goroutine, so the paths need a lock
	var mu sync.Mutex
	var paths []string
	counts := make(map[string]int)

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		n := counts[r.URL.Path]
		counts[r.URL.Path]++
		mu.Unlock()

		if got, want := r.Header.Get("Authorization"), "Bearer "+initTestToken; got != want {
			t.Errorf("Authorization header = %q, want %q", got, want)
		}

		var res notionResponse
		sequence, ok := responses[r.URL.Path]
		if ok {
			res = sequence[min(n, len(sequence)-1)]
		}
		if !ok && r.URL.Path == usersMePath {
			res, ok = tokenValid, true
		}
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

// The token is checked first, so a failed check looks nothing up in Notion and leaves everything
// stored as it was, even with every flag given (DESIGN.md §5)
func TestRun_Init_TokenCheckFails_StopsBeforeLookups(t *testing.T) {
	unauthorized := notionResponse{http.StatusUnauthorized, `{"object": "error", "status": 401, "code": "unauthorized", "message": "API token is invalid."}`}
	serverError := notionResponse{http.StatusInternalServerError, `{"object": "error", "status": 500, "code": "internal_server_error", "message": "Unexpected error occurred."}`}

	tests := []struct {
		name        string
		response    notionResponse
		storedTasks string
		storedProj  string
		wantExit    int
		wantCode    clierr.ErrorCode
	}{
		{
			name:     "invalid_token_nothing_stored",
			response: unauthorized,
			wantExit: 5,
			wantCode: clierr.AccessDenied,
		},
		{
			name:        "invalid_token_already_connected",
			response:    unauthorized,
			storedTasks: "old-tasks-ds",
			storedProj:  "old-projects-ds",
			wantExit:    5,
			wantCode:    clierr.AccessDenied,
		},
		// Edge cases
		{
			// Not a verdict on the token, but init still can't go on
			name:     "server_error",
			response: serverError,
			wantExit: 10,
			wantCode: clierr.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Any request past the token check fails the test as unexpected
			s, requested := fakeNotion(t, map[string]notionResponse{usersMePath: tc.response})
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, tc.storedTasks, tc.storedProj)

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage("--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID)...)

			checkInitError(t, exit, stdout, stderr, tc.wantExit, tc.wantCode)

			want := []string{usersMePath}
			if got := requested(); !slices.Equal(got, want) {
				t.Errorf("paths requested = %v, want %v", got, want)
			}

			gotTasks, gotProjects := storedDataSources(t, dbPath)
			if gotTasks != tc.storedTasks {
				t.Errorf("stored tasks data source = %q, want %q", gotTasks, tc.storedTasks)
			}
			if gotProjects != tc.storedProj {
				t.Errorf("stored projects data source = %q, want %q", gotProjects, tc.storedProj)
			}
			checkEntriesDataSource(t, dbPath, "")
			if got := storedContext(t, dbPath).PropertyMap; got != nil {
				t.Errorf("stored property map = %v, want none", got)
			}
			checkCacheStateUntouched(t, dbPath)
		})
	}
}

// Each data source is the one looked up if its database was given, otherwise the stored one, and
// the result is what gets reported and saved (DESIGN.md §5)
func TestRun_Init_ResolvesDataSources(t *testing.T) {
	tasksDataSourcePath := "/data_sources/" + mapTasksDataSourceID
	tasksQueryPath := tasksDataSourcePath + "/query"
	projectsQueryPath := "/data_sources/" + mapProjectsDataSourceID + "/query"
	noPages := notionResponse{http.StatusOK, `{"object": "list", "results": [], "next_cursor": null, "has_more": false}`}

	// Every run checks the token, reads the tasks schema, refreshes both data sources and creates
	// the entries database. Sorted, the database lookups fall between these and the token check
	requestsWith := func(lookups ...string) []string {
		requests := append([]string{projectsQueryPath, tasksDataSourcePath, tasksQueryPath, createDatabasePath}, lookups...)
		return append(requests, usersMePath)
	}

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
			wantRequests: requestsWith(projectsDatabasePath, tasksDatabasePath),
		},
		{
			// A database given in this run replaces what's stored
			name:         "both_given_both_stored",
			args:         []string{"--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID},
			storedTasks:  "old-tasks-ds",
			storedProj:   "old-projects-ds",
			wantRequests: requestsWith(projectsDatabasePath, tasksDatabasePath),
		},
		{
			name:         "tasks_given_projects_stored",
			args:         []string{"--tasks-db", tasksDatabaseID},
			storedTasks:  "old-tasks-ds",
			storedProj:   mapProjectsDataSourceID,
			wantRequests: requestsWith(tasksDatabasePath),
		},
		{
			name:         "projects_given_tasks_stored",
			args:         []string{"--projects-db", projectsDatabaseID},
			storedTasks:  mapTasksDataSourceID,
			storedProj:   "old-projects-ds",
			wantRequests: requestsWith(projectsDatabasePath),
		},
		{
			name:         "none_given_both_stored",
			storedTasks:  mapTasksDataSourceID,
			storedProj:   mapProjectsDataSourceID,
			wantRequests: requestsWith(),
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
				tasksQueryPath:     noPages,
				projectsQueryPath:  noPages,
				createDatabasePath: databaseCreated,
			})
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, tc.storedTasks, tc.storedProj)

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage(tc.args...)...)
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
			name:         "nothing_given_nothing_stored",
			wantRequests: []string{usersMePath},
		},
		{
			name:         "tasks_given_nothing_stored",
			args:         []string{"--tasks-db", tasksDatabaseID},
			wantRequests: []string{tasksDatabasePath, usersMePath},
		},
		{
			name:         "projects_given_nothing_stored",
			args:         []string{"--projects-db", projectsDatabaseID},
			wantRequests: []string{projectsDatabasePath, usersMePath},
		},
		{
			name:         "only_tasks_stored",
			storedTasks:  mapTasksDataSourceID,
			wantRequests: []string{usersMePath},
		},
		{
			name:         "only_projects_stored",
			storedProj:   mapProjectsDataSourceID,
			wantRequests: []string{usersMePath},
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
		{name: "tasks_set_parent_page_empty", args: []string{"--tasks-db", tasksDatabaseID, "--parent-page", ""}},
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
			name:        "parent_page_empty",
			token:       ptr("test-token"),
			args:        []string{"init", "--parent-page", ""},
			wantExit:    2,
			wantCode:    clierr.Usage,
			wantMessage: "--parent-page",
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
		{
			name: "several_relations_to_projects_prefers_project_name",
			properties: []string{
				titleProperty("Task"),
				relationProperty("Client work", mapProjectsDataSourceID),
				relationProperty("Project", mapProjectsDataSourceID),
				relationProperty("Related projects", mapProjectsDataSourceID),
				doneProperty,
			},
			wantProject: "Project",
		},
		{
			name: "project_name_preferred_ignoring_case",
			properties: []string{
				titleProperty("Task"),
				relationProperty("Client work", mapProjectsDataSourceID),
				relationProperty("PROJECT", mapProjectsDataSourceID),
				doneProperty,
			},
			wantProject: "PROJECT",
		},
		{
			// The name only breaks a tie between relations that target projects
			name: "project_name_ignored_when_it_targets_another_data_source",
			properties: []string{
				titleProperty("Task"),
				relationProperty("Project", mapOtherDataSourceID),
				relationProperty("Client work", mapProjectsDataSourceID),
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

// fullTasksSchema is a tasks data source with every property init looks for
func fullTasksSchema() []string {
	return []string{
		titleProperty("Task"),
		relationProperty("Project", mapProjectsDataSourceID),
		orderProperty,
		doneProperty,
	}
}

// projectPage is a page from the projects data source, as a query returns it
func projectPage(id, name string) string {
	return fmt.Sprintf(`{"object": "page", "id": %q, "created_time": "2026-11-02T09:00:00.000Z", "last_edited_time": "2026-11-02T09:00:00.000Z", "in_trash": false, "properties": {"Name": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": %q}]}}}`, id, name)
}

// connectedResponses answers everything init asks for on a first run: both database lookups, the
// tasks schema, a refresh that finds no tasks and the given project pages, and creating the entries
// database. A test about a stored entries database adds entriesDataSourcePath, and a test about a
// failed create replaces createDatabasePath
func connectedResponses(schema []string, projectPages ...string) map[string]notionResponse {
	return map[string]notionResponse{
		tasksDatabasePath:                                     databaseWithDataSource(mapTasksDataSourceID),
		projectsDatabasePath:                                  databaseWithDataSource(mapProjectsDataSourceID),
		"/data_sources/" + mapTasksDataSourceID:               tasksSchema(schema),
		"/data_sources/" + mapTasksDataSourceID + "/query":    {http.StatusOK, `{"object": "list", "results": [], "next_cursor": null, "has_more": false}`},
		"/data_sources/" + mapProjectsDataSourceID + "/query": {http.StatusOK, fmt.Sprintf(`{"object": "list", "results": [%s], "next_cursor": null, "has_more": false}`, strings.Join(projectPages, ", "))},
		createDatabasePath:                                    databaseCreated,
	}
}

// connectedNotion is a fake Notion answering with connectedResponses
func connectedNotion(t *testing.T, schema []string, projectPages ...string) (*httptest.Server, func() []string) {
	t.Helper()

	return fakeNotion(t, connectedResponses(schema, projectPages...))
}

// entriesSchema is the response for the stored entries data source
func entriesSchema(inTrash bool, properties ...string) notionResponse {
	return notionResponse{http.StatusOK, fmt.Sprintf(`{"object": "data_source", "id": %q, "in_trash": %t, "properties": {%s}}`, entriesDataSourceID, inTrash, strings.Join(properties, ", "))}
}

// entriesLinkedTo is a live entries data source whose Task relation targets tasksDataSourceID
func entriesLinkedTo(tasksDataSourceID string) notionResponse {
	return entriesSchema(false, titleProperty("Title"), relationProperty("Task", tasksDataSourceID))
}

// seedEntriesDataSource stores the entries data source ID in the default context
func seedEntriesDataSource(t *testing.T, dbPath string) {
	t.Helper()

	onStore(t, dbPath, func(st *store.Store) {
		nc, err := st.NotionContext(t.Context(), store.DefaultContextID)
		if err != nil {
			t.Fatalf("NotionContext(): %v", err)
		}
		id := entriesDataSourceID
		nc.EntriesDataSourceID = &id
		if err := st.SaveNotionContext(t.Context(), nc); err != nil {
			t.Fatalf("SaveNotionContext(): %v", err)
		}
	})
}

// checkEntriesDataSource fails the test unless the stored entries data source ID is want, empty
// meaning unset
func checkEntriesDataSource(t *testing.T, dbPath, want string) {
	t.Helper()

	var got string
	if id := storedContext(t, dbPath).EntriesDataSourceID; id != nil {
		got = *id
	}
	if got != want {
		t.Errorf("stored entries data source = %q, want %q", got, want)
	}
}

// onStore runs fn against the database at dbPath, for seeding it or reading it back
func onStore(t *testing.T, dbPath string, fn func(st *store.Store)) {
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

	fn(st)
}

// seedDefaultProject stores a default project in the default context
func seedDefaultProject(t *testing.T, dbPath, pageID string) {
	t.Helper()

	onStore(t, dbPath, func(st *store.Store) {
		nc, err := st.NotionContext(t.Context(), store.DefaultContextID)
		if err != nil {
			t.Fatalf("NotionContext(): %v", err)
		}
		nc.DefaultProjectPageID = &pageID
		if err := st.SaveNotionContext(t.Context(), nc); err != nil {
			t.Fatalf("SaveNotionContext(): %v", err)
		}
	})
}

// storedContext reads back the default context
func storedContext(t *testing.T, dbPath string) store.NotionContext {
	t.Helper()

	var nc store.NotionContext
	onStore(t, dbPath, func(st *store.Store) {
		var err error
		nc, err = st.NotionContext(t.Context(), store.DefaultContextID)
		if err != nil {
			t.Fatalf("NotionContext(): %v", err)
		}
	})
	return nc
}

// init always does a full refresh, however recent the last one was (DESIGN.md §5). Only a full
// refresh marks a task gone, so a cached task Notion no longer returns shows which one ran
func TestRun_Init_DoesFullRefresh(t *testing.T) {
	s, _ := connectedNotion(t, fullTasksSchema())
	dbPath := filepath.Join(t.TempDir(), "test.db")
	seedDataSources(t, dbPath, mapTasksDataSourceID, mapProjectsDataSourceID)

	lastRefreshed := time.Now().UTC().Add(-time.Minute)
	onStore(t, dbPath, func(st *store.Store) {
		task := store.Task{ContextID: store.DefaultContextID, PageID: "oldtaskpage", Name: "From before", LastEditedTime: lastRefreshed}
		if _, err := st.RefreshTasks(t.Context(), []store.Task{task}, lastRefreshed, false); err != nil {
			t.Fatalf("RefreshTasks(): %v", err)
		}
	})

	exit, _, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage()...)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", exit, stderr)
	}

	onStore(t, dbPath, func(st *store.Store) {
		state, err := st.CacheState(t.Context())
		if err != nil {
			t.Fatalf("CacheState(): %v", err)
		}
		if state.TasksFullRefreshedAt == nil {
			t.Error("tasks_full_refreshed_at = null, want it set")
		}

		tasks, _, err := st.ListTasks(t.Context(), store.NoLimit, true, nil)
		if err != nil {
			t.Fatalf("ListTasks(): %v", err)
		}
		if len(tasks) != 0 {
			t.Errorf("listed %d tasks, want the cached one marked gone", len(tasks))
		}
	})
}

func TestRun_Init_DefaultProject(t *testing.T) {
	const (
		sabbaticalPageID = "sabbaticalpage"
		storedDefault    = "storeddefaultpage"
	)

	tests := []struct {
		name           string
		args           []string
		storedProjects string
		storedDefault  string
		wantDefault    string // empty means none
	}{
		{
			name:           "name_given",
			args:           []string{"--project", "Sabbatical"},
			storedProjects: mapProjectsDataSourceID,
			wantDefault:    sabbaticalPageID,
		},
		{
			name:           "name_matched_ignoring_case",
			args:           []string{"--project", "sabbatical"},
			storedProjects: mapProjectsDataSourceID,
			wantDefault:    sabbaticalPageID,
		},
		{
			name:           "name_given_replaces_stored_default",
			args:           []string{"--project", "Sabbatical"},
			storedProjects: mapProjectsDataSourceID,
			storedDefault:  storedDefault,
			wantDefault:    sabbaticalPageID,
		},
		{
			name:           "no_name_keeps_stored_default",
			storedProjects: mapProjectsDataSourceID,
			storedDefault:  storedDefault,
			wantDefault:    storedDefault,
		},
		{
			// The database is looked up again, but its data source hasn't changed
			name:           "same_projects_database_keeps_stored_default",
			args:           []string{"--projects-db", projectsDatabaseID},
			storedProjects: mapProjectsDataSourceID,
			storedDefault:  storedDefault,
			wantDefault:    storedDefault,
		},
		{
			name:           "projects_data_source_changed_clears_stored_default",
			args:           []string{"--projects-db", projectsDatabaseID},
			storedProjects: "old-projects-ds",
			storedDefault:  storedDefault,
		},
		{
			name:           "projects_data_source_changed_with_name_given",
			args:           []string{"--projects-db", projectsDatabaseID, "--project", "Sabbatical"},
			storedProjects: "old-projects-ds",
			storedDefault:  storedDefault,
			wantDefault:    sabbaticalPageID,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := connectedNotion(t, fullTasksSchema(), projectPage(sabbaticalPageID, "Sabbatical"))
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, mapTasksDataSourceID, tc.storedProjects)
			if tc.storedDefault != "" {
				seedDefaultProject(t, dbPath, tc.storedDefault)
			}

			exit, _, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage(tc.args...)...)
			if exit != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", exit, stderr)
			}

			var got string
			if d := storedContext(t, dbPath).DefaultProjectPageID; d != nil {
				got = *d
			}
			if got != tc.wantDefault {
				t.Errorf("default project = %q, want %q", got, tc.wantDefault)
			}
		})
	}
}

// A wrong name stops init before anything is created in Notion or saved to the context, so a re-run
// is enough (DESIGN.md §5)
func TestRun_Init_DefaultProject_Errors(t *testing.T) {
	const storedDefault = "storeddefaultpage"

	tests := []struct {
		name           string
		project        string
		wantCode       clierr.ErrorCode
		wantCandidates []output.ProjectRef
	}{
		{
			name:     "no_match",
			project:  "Missing",
			wantCode: clierr.NotFound,
		},
		{
			// It's cached, but this init's refresh finds it has gone from Notion
			name:     "project_gone_from_notion",
			project:  "Deleted",
			wantCode: clierr.NotFound,
		},
		{
			name:     "several_match",
			project:  "admin",
			wantCode: clierr.AmbiguousSelector,
			wantCandidates: []output.ProjectRef{
				{PageID: "adminpageupper", Name: "Admin"},
				{PageID: "adminpagelower", Name: "admin"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, requested := connectedNotion(t, fullTasksSchema(),
				projectPage("adminpageupper", "Admin"),
				projectPage("adminpagelower", "admin"))
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, mapTasksDataSourceID, "old-projects-ds")
			seedDefaultProject(t, dbPath, storedDefault)
			onStore(t, dbPath, func(st *store.Store) {
				deleted := store.Project{ContextID: store.DefaultContextID, PageID: "deletedpage", Name: "Deleted"}
				if _, err := st.RefreshProjects(t.Context(), []store.Project{deleted}, time.Now().UTC()); err != nil {
					t.Fatalf("RefreshProjects(): %v", err)
				}
			})

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage("--projects-db", projectsDatabaseID, "--project", tc.project)...)

			checkInitError(t, exit, stdout, stderr, 3, tc.wantCode)

			if got := requestCount(requested(), createDatabasePath); got != 0 {
				t.Errorf("created the entries database %d times, want none", got)
			}
			checkEntriesDataSource(t, dbPath, "")

			var doc struct {
				Error struct {
					Details struct {
						Selector   string              `json:"selector"`
						Candidates []output.ProjectRef `json:"candidates"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr, err)
			}
			if doc.Error.Details.Selector != tc.project {
				t.Errorf("selector = %q, want %q", doc.Error.Details.Selector, tc.project)
			}
			if !slices.Equal(doc.Error.Details.Candidates, tc.wantCandidates) {
				t.Errorf("candidates = %v, want %v", doc.Error.Details.Candidates, tc.wantCandidates)
			}

			nc := storedContext(t, dbPath)
			if nc.ProjectsDataSourceID == nil || *nc.ProjectsDataSourceID != "old-projects-ds" {
				t.Errorf("stored projects data source = %v, want it unchanged", nc.ProjectsDataSourceID)
			}
			if nc.DefaultProjectPageID == nil || *nc.DefaultProjectPageID != storedDefault {
				t.Errorf("stored default project = %v, want it unchanged", nc.DefaultProjectPageID)
			}
			if nc.PropertyMap != nil {
				t.Errorf("stored property map = %v, want none", nc.PropertyMap)
			}
		})
	}
}

func TestRun_Init_SavesPropertyMapAndReportsWarnings(t *testing.T) {
	tests := []struct {
		name         string
		schema       []string
		wantMap      map[string]string
		wantWarnings []output.WarningCode
	}{
		{
			name:    "all_properties_found",
			schema:  fullTasksSchema(),
			wantMap: map[string]string{"project": "Project", "order": "Order", "done": "Done"},
		},
		{
			name: "renamed_relation",
			schema: []string{
				titleProperty("Name"),
				relationProperty("Client work", mapProjectsDataSourceID),
				orderProperty,
				doneProperty,
			},
			wantMap: map[string]string{"project": "Client work", "order": "Order", "done": "Done"},
		},
		{
			name: "no_order",
			schema: []string{
				titleProperty("Task"),
				relationProperty("Project", mapProjectsDataSourceID),
				doneProperty,
			},
			wantMap:      map[string]string{"project": "Project", "done": "Done"},
			wantWarnings: []output.WarningCode{output.OrderPropertyMissing},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := connectedNotion(t, tc.schema)
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, mapTasksDataSourceID, mapProjectsDataSourceID)

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage()...)
			if exit != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", exit, stderr)
			}

			if got := storedContext(t, dbPath).PropertyMap; !maps.Equal(got, tc.wantMap) {
				t.Errorf("stored property map = %v, want %v", got, tc.wantMap)
			}

			var doc struct {
				Warnings []struct {
					Code    output.WarningCode `json:"code"`
					Details struct {
						DataSourceID string `json:"data_source_id"`
					} `json:"details"`
				} `json:"warnings"`
			}
			if err := json.Unmarshal(stdout, &doc); err != nil {
				t.Fatalf("unable to decode document %q: %v", stdout, err)
			}

			var codes []output.WarningCode
			for _, w := range doc.Warnings {
				codes = append(codes, w.Code)
				if w.Details.DataSourceID != mapTasksDataSourceID {
					t.Errorf("warning %q data_source_id = %q, want %q", w.Code, w.Details.DataSourceID, mapTasksDataSourceID)
				}
			}
			if !slices.Equal(codes, tc.wantWarnings) {
				t.Errorf("warning codes = %v, want %v", codes, tc.wantWarnings)
			}
		})
	}
}

// A required property that's missing stops init before it refreshes or saves anything (DESIGN.md §5)
func TestRun_Init_RequiredPropertyMissing(t *testing.T) {
	tests := []struct {
		name   string
		schema []string
	}{
		{
			name: "no_project_relation",
			schema: []string{
				titleProperty("Task"),
				relationProperty("Owner", mapOtherDataSourceID),
				doneProperty,
			},
		},
		{
			name: "no_done",
			schema: []string{
				titleProperty("Task"),
				relationProperty("Project", mapProjectsDataSourceID),
				orderProperty,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, requested := connectedNotion(t, tc.schema)
			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, mapTasksDataSourceID, mapProjectsDataSourceID)

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client())

			checkInitError(t, exit, stdout, stderr, 3, clierr.NotFound)

			var doc struct {
				Error struct {
					Details struct {
						DataSourceID string `json:"data_source_id"`
					} `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr, err)
			}
			if doc.Error.Details.DataSourceID != mapTasksDataSourceID {
				t.Errorf("data_source_id = %q, want %q", doc.Error.Details.DataSourceID, mapTasksDataSourceID)
			}

			want := []string{"/data_sources/" + mapTasksDataSourceID, usersMePath}
			if got := requested(); !slices.Equal(got, want) {
				t.Errorf("paths requested = %v, want %v", got, want)
			}
			if got := storedContext(t, dbPath).PropertyMap; got != nil {
				t.Errorf("stored property map = %v, want none", got)
			}
			checkCacheStateUntouched(t, dbPath)
		})
	}
}

// Several relations to projects, none named Project, can't be told apart, so init lists them and
// stops before it refreshes or saves anything. Their order is fixed because the schema's isn't
// (DESIGN.md §5, §8)
func TestRun_Init_SeveralProjectRelations_ReturnsCandidates(t *testing.T) {
	s, requested := connectedNotion(t, []string{
		titleProperty("Task"),
		relationProperty("Related projects", mapProjectsDataSourceID),
		relationProperty("Client work", mapProjectsDataSourceID),
		relationProperty("Another", mapProjectsDataSourceID),
		relationProperty("Owner", mapOtherDataSourceID),
		doneProperty,
	})
	dbPath := filepath.Join(t.TempDir(), "test.db")
	seedDataSources(t, dbPath, mapTasksDataSourceID, mapProjectsDataSourceID)

	exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client())

	checkInitError(t, exit, stdout, stderr, 3, clierr.AmbiguousSelector)

	var doc struct {
		Error struct {
			Details struct {
				Selector   string          `json:"selector"`
				Candidates json.RawMessage `json:"candidates"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr, &doc); err != nil {
		t.Fatalf("unable to decode error document %q: %v", stderr, err)
	}
	if doc.Error.Details.Selector != mapTasksDataSourceID {
		t.Errorf("selector = %q, want %q", doc.Error.Details.Selector, mapTasksDataSourceID)
	}
	// Compared as JSON because the shape of each candidate is part of the contract
	wantCandidates := `[{"name":"Another"},{"name":"Client work"},{"name":"Related projects"}]`
	if got := string(doc.Error.Details.Candidates); got != wantCandidates {
		t.Errorf("candidates = %s, want %s", got, wantCandidates)
	}

	want := []string{"/data_sources/" + mapTasksDataSourceID, usersMePath}
	if got := requested(); !slices.Equal(got, want) {
		t.Errorf("paths requested = %v, want %v", got, want)
	}
	if got := storedContext(t, dbPath).PropertyMap; got != nil {
		t.Errorf("stored property map = %v, want none", got)
	}
	checkCacheStateUntouched(t, dbPath)
}

// With no entries database yet, init creates one and both reports and saves the data source from
// the response (DESIGN.md §5)
func TestRun_Init_CreatesEntriesDatabase(t *testing.T) {
	s, requested := connectedNotion(t, fullTasksSchema())
	dbPath := filepath.Join(t.TempDir(), "test.db")
	seedDataSources(t, dbPath, mapTasksDataSourceID, mapProjectsDataSourceID)

	exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage()...)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", exit, stderr)
	}

	if got := requestCount(requested(), createDatabasePath); got != 1 {
		t.Errorf("created the entries database %d times, want 1", got)
	}

	var doc struct {
		Context struct {
			EntriesDataSourceID string `json:"entries_data_source_id"`
		} `json:"context"`
	}
	if err := json.Unmarshal(stdout, &doc); err != nil {
		t.Fatalf("unable to decode document %q: %v", stdout, err)
	}
	if doc.Context.EntriesDataSourceID != entriesDataSourceID {
		t.Errorf("output entries data source = %q, want %q", doc.Context.EntriesDataSourceID, entriesDataSourceID)
	}
	checkEntriesDataSource(t, dbPath, entriesDataSourceID)
}

// A parent page is only needed to create the database, so nothing else has to be checked first. The
// check comes before the refresh, so a forgotten flag doesn't cost one
func TestRun_Init_NoEntriesDatabaseNoParentPage_ReturnsNotInitialised(t *testing.T) {
	s, requested := connectedNotion(t, fullTasksSchema())
	dbPath := filepath.Join(t.TempDir(), "test.db")
	seedDataSources(t, dbPath, mapTasksDataSourceID, mapProjectsDataSourceID)

	exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client())

	checkInitError(t, exit, stdout, stderr, 5, clierr.NotInitialised)

	if !bytes.Contains(stderr, []byte("--parent-page")) {
		t.Errorf("error doesn't mention --parent-page: %s", stderr)
	}

	want := []string{"/data_sources/" + mapTasksDataSourceID, usersMePath}
	if got := requested(); !slices.Equal(got, want) {
		t.Errorf("paths requested = %v, want %v", got, want)
	}
	checkEntriesDataSource(t, dbPath, "")
	if got := storedContext(t, dbPath).PropertyMap; got != nil {
		t.Errorf("stored property map = %v, want none", got)
	}
	checkCacheStateUntouched(t, dbPath)
}

// A stored entries database that's still usable is reused: re-running init never creates another
// (DESIGN.md §5)
func TestRun_Init_ReusesEntriesDatabase(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "nothing_given"},
		{name: "parent_page_given_is_ignored", args: withParentPage()},
		{
			// The database is looked up again, but its data source hasn't changed
			name: "same_tasks_database_given",
			args: []string{"--tasks-db", tasksDatabaseID},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			responses := connectedResponses(fullTasksSchema())
			responses[entriesDataSourcePath] = entriesLinkedTo(mapTasksDataSourceID)
			s, requested := fakeNotion(t, responses)

			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, mapTasksDataSourceID, mapProjectsDataSourceID)
			seedEntriesDataSource(t, dbPath)

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), tc.args...)
			if exit != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", exit, stderr)
			}

			got := requested()
			if n := requestCount(got, createDatabasePath); n != 0 {
				t.Errorf("created the entries database %d times, want none", n)
			}
			if n := requestCount(got, entriesDataSourcePath); n != 1 {
				t.Errorf("checked the entries data source %d times, want 1", n)
			}

			var doc struct {
				Context struct {
					EntriesDataSourceID string `json:"entries_data_source_id"`
				} `json:"context"`
			}
			if err := json.Unmarshal(stdout, &doc); err != nil {
				t.Fatalf("unable to decode document %q: %v", stdout, err)
			}
			if doc.Context.EntriesDataSourceID != entriesDataSourceID {
				t.Errorf("output entries data source = %q, want %q", doc.Context.EntriesDataSourceID, entriesDataSourceID)
			}
			checkEntriesDataSource(t, dbPath, entriesDataSourceID)
		})
	}
}

// The ID the first run saves is the one the second run checks, so it doesn't create a second database
func TestRun_Init_Rerun_DoesNotCreateAgain(t *testing.T) {
	responses := connectedResponses(fullTasksSchema())
	responses[entriesDataSourcePath] = entriesLinkedTo(mapTasksDataSourceID)
	s, requested := fakeNotion(t, responses)

	dbPath := filepath.Join(t.TempDir(), "test.db")
	seedDataSources(t, dbPath, mapTasksDataSourceID, mapProjectsDataSourceID)

	for _, args := range [][]string{withParentPage(), nil} {
		exit, _, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), args...)
		if exit != 0 {
			t.Fatalf("init %v: exit code = %d, want 0 (stderr: %q)", args, exit, stderr)
		}
	}

	if got := requestCount(requested(), createDatabasePath); got != 1 {
		t.Errorf("created the entries database %d times, want 1", got)
	}
	checkEntriesDataSource(t, dbPath, entriesDataSourceID)
}

// Entries timed before connecting stay as they were, and init reports them as waiting (DESIGN.md §4, §5)
func TestRun_Init_ReportsWaitingEntries(t *testing.T) {
	tests := []struct {
		name   string
		before [][]string
		want   float64
	}{
		{name: "no_entries", want: 0},
		{
			name:   "stopped_and_running",
			before: [][]string{{"start", "first"}, {"stop"}, {"start", "second"}},
			want:   2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := connectedNotion(t, fullTasksSchema())
			dbPath := filepath.Join(t.TempDir(), "test.db")

			for _, args := range tc.before {
				exit, _, stderr := runAgainstNotion(t, dbPath, s.URL, s.Client(), args...)
				if exit != 0 {
					t.Fatalf("%v: exit code = %d, want 0 (stderr: %q)", args, exit, stderr)
				}
			}

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage("--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID)...)
			if exit != 0 {
				t.Fatalf("exit code = %d, want 0 (stderr: %q)", exit, stderr)
			}

			// Decoded into a map because 0 is the zero value, so a struct couldn't show the key is present
			var doc map[string]any
			if err := json.Unmarshal(stdout, &doc); err != nil {
				t.Fatalf("unable to decode document %q: %v", stdout, err)
			}
			if got, ok := doc["waiting"]; !ok || got != tc.want {
				t.Errorf("waiting = %v (present: %v), want %v", got, ok, tc.want)
			}
		})
	}
}

// storedEntries reads back the entries started in the last day, oldest first
func storedEntries(t *testing.T, dbPath string) []store.Entry {
	t.Helper()

	now := time.Now()
	var entries []store.Entry
	onStore(t, dbPath, func(st *store.Store) {
		status, err := st.ReadStatus(t.Context(), now.Add(-24*time.Hour), now.Add(24*time.Hour))
		if err != nil {
			t.Fatalf("ReadStatus(): %v", err)
		}
		entries = status.TodayEntries
	})
	return entries
}

// entriesJSON gives entries as text, because comparing them directly would compare the addresses
// of their pointer fields, and a failure would print those addresses too
func entriesJSON(t *testing.T, entries []store.Entry) string {
	t.Helper()

	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("encoding entries: %v", err)
	}
	return string(b)
}

// Connecting a context that's been used offline changes none of its entries: they stay as recorded,
// with no task, and a running one keeps running (DESIGN.md §5)
func TestRun_Init_EntriesRecordedBeforeInit_StayIntactAndUnattached(t *testing.T) {
	s, _ := connectedNotion(t, fullTasksSchema())
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// The first entry is backdated so that the two can't start at the same moment
	recorded := [][]string{{"start", "first", "--ago", "2h"}, {"stop"}, {"start", "second"}}
	for _, args := range recorded {
		exit, _, stderr := runAgainstNotion(t, dbPath, s.URL, s.Client(), args...)
		if exit != 0 {
			t.Fatalf("%v: exit code = %d, want 0 (stderr: %q)", args, exit, stderr)
		}
	}
	before := storedEntries(t, dbPath)

	exit, _, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage("--tasks-db", tasksDatabaseID, "--projects-db", projectsDatabaseID)...)
	if exit != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", exit, stderr)
	}

	after := storedEntries(t, dbPath)

	// Every field, so a change to the times, sync state or updated_at is caught as well
	if got, want := entriesJSON(t, after), entriesJSON(t, before); got != want {
		t.Errorf("entries after init:\n got %s\nwant %s", got, want)
	}

	if len(after) != 2 {
		t.Fatalf("got %d entries, want 2: %s", len(after), entriesJSON(t, after))
	}

	wantEntries := []struct {
		description string
		running     bool
	}{
		{description: "first", running: false},
		{description: "second", running: true},
	}
	for i, want := range wantEntries {
		e := after[i]

		if e.Description == nil || *e.Description != want.description {
			t.Errorf("entry %d description isn't %q: %s", i, want.description, entriesJSON(t, after))
		}
		if running := e.EndUTC == nil; running != want.running {
			t.Errorf("entry %d running = %v, want %v", i, running, want.running)
		}
		if e.TaskPageID != nil {
			t.Errorf("entry %d task = %q, want none", i, *e.TaskPageID)
		}
		if e.EntryPageID != nil {
			t.Errorf("entry %d Notion row = %q, want none", i, *e.EntryPageID)
		}
		if e.SyncState != "pending" {
			t.Errorf("entry %d sync state = %q, want %q", i, e.SyncState, "pending")
		}
	}
}

// A stored entries database that can't be used stops init before it refreshes or saves anything.
// Every case passes --parent-page, because replacing the database could strand rows already synced,
// so the flag must never create a new one
func TestRun_Init_StoredEntriesDatabaseUnusable(t *testing.T) {
	const oldTasksDataSourceID = "old-tasks-ds"

	tests := []struct {
		name        string
		args        []string
		storedTasks string
		entries     notionResponse
		wantExit    int
		wantCode    clierr.ErrorCode
		wantMessage string // part of the error message; empty skips the check
		wantDetails bool   // details should name the entries data source
		// Details saying what the relation targets and what it should. Empty means no such key,
		// because a property that isn't a relation has no target to report
		wantRelationID string
		wantTasksID    string
	}{
		{
			name:        "in_trash",
			storedTasks: mapTasksDataSourceID,
			entries:     entriesSchema(true, titleProperty("Title"), relationProperty("Task", mapTasksDataSourceID)),
			wantExit:    5,
			wantCode:    clierr.NotInitialised,
			wantMessage: "trash",
			wantDetails: true,
		},
		{
			// The stored database still links to the tasks database this run replaces
			name:           "tasks_database_changed",
			args:           []string{"--tasks-db", tasksDatabaseID},
			storedTasks:    oldTasksDataSourceID,
			entries:        entriesLinkedTo(oldTasksDataSourceID),
			wantExit:       5,
			wantCode:       clierr.NotInitialised,
			wantMessage:    "different tasks database",
			wantDetails:    true,
			wantRelationID: oldTasksDataSourceID,
			wantTasksID:    mapTasksDataSourceID,
		},
		{
			name:           "task_relation_points_elsewhere",
			storedTasks:    mapTasksDataSourceID,
			entries:        entriesLinkedTo(mapOtherDataSourceID),
			wantExit:       5,
			wantCode:       clierr.NotInitialised,
			wantMessage:    "different tasks database",
			wantDetails:    true,
			wantRelationID: mapOtherDataSourceID,
			wantTasksID:    mapTasksDataSourceID,
		},
		{
			// Renamed or deleted in Notion
			name:        "task_property_missing",
			storedTasks: mapTasksDataSourceID,
			entries:     entriesSchema(false, titleProperty("Title")),
			wantExit:    5,
			wantCode:    clierr.NotInitialised,
			wantMessage: "has no",
			wantDetails: true,
		},
		{
			// Has no target at all, which mustn't read as linking to a different database
			name:        "task_property_not_a_relation",
			storedTasks: mapTasksDataSourceID,
			entries:     entriesSchema(false, titleProperty("Title"), `"Task": {"id": "tsk", "name": "Task", "type": "rich_text", "rich_text": {}}`),
			wantExit:    5,
			wantCode:    clierr.NotInitialised,
			wantMessage: "has no",
			wantDetails: true,
		},
		{
			// What Notion returns for a database that's been deleted or un-shared. The message isn't
			// checked yet: printError drops the text init wraps this error with (see issue #20)
			name:        "not_found",
			storedTasks: mapTasksDataSourceID,
			entries:     notionResponse{http.StatusNotFound, `{"object": "error", "status": 404, "code": "object_not_found", "message": "Could not find data source."}`},
			wantExit:    3,
			wantCode:    clierr.NotFound,
		},
		// Other failures keep their own code, because they don't mean the database has gone
		{
			name:        "invalid_token",
			storedTasks: mapTasksDataSourceID,
			entries:     notionResponse{http.StatusUnauthorized, `{"object": "error", "status": 401, "code": "unauthorized", "message": "API token is invalid."}`},
			wantExit:    5,
			wantCode:    clierr.AccessDenied,
		},
		{
			name:        "server_error",
			storedTasks: mapTasksDataSourceID,
			entries:     notionResponse{http.StatusInternalServerError, `{"object": "error", "status": 500, "code": "internal_server_error", "message": "Unexpected error occurred."}`},
			wantExit:    10,
			wantCode:    clierr.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			responses := connectedResponses(fullTasksSchema())
			responses[entriesDataSourcePath] = tc.entries
			s, requested := fakeNotion(t, responses)

			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, tc.storedTasks, mapProjectsDataSourceID)
			seedEntriesDataSource(t, dbPath)

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage(tc.args...)...)

			checkInitError(t, exit, stdout, stderr, tc.wantExit, tc.wantCode)

			var doc struct {
				Error struct {
					Message string `json:"message"`
					// A map, because a struct can't tell a missing key from an empty value, and
					// which keys appear is part of the contract (DESIGN.md §8)
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr, err)
			}
			if !strings.Contains(doc.Error.Message, tc.wantMessage) {
				t.Errorf("error message = %q, want it to include %q", doc.Error.Message, tc.wantMessage)
			}
			if id := doc.Error.Details["entries_data_source_id"]; tc.wantDetails && id != entriesDataSourceID {
				t.Errorf("details entries_data_source_id = %v, want %q", id, entriesDataSourceID)
			}
			for key, want := range map[string]string{
				"task_relation_data_source_id": tc.wantRelationID,
				"tasks_data_source_id":         tc.wantTasksID,
			} {
				got, present := doc.Error.Details[key]
				if want == "" {
					if present {
						t.Errorf("details %s = %v, want no such key", key, got)
					}
					continue
				}
				if got != want {
					t.Errorf("details %s = %v, want %q", key, got, want)
				}
			}

			if got := requestCount(requested(), createDatabasePath); got != 0 {
				t.Errorf("created the entries database %d times, want none", got)
			}
			checkCacheStateUntouched(t, dbPath)

			checkEntriesDataSource(t, dbPath, entriesDataSourceID)
			gotTasks, _ := storedDataSources(t, dbPath)
			if gotTasks != tc.storedTasks {
				t.Errorf("stored tasks data source = %q, want %q", gotTasks, tc.storedTasks)
			}
			if got := storedContext(t, dbPath).PropertyMap; got != nil {
				t.Errorf("stored property map = %v, want none", got)
			}
		})
	}
}

// A failed create saves nothing to the context, so re-running init tries again (DESIGN.md §5)
func TestRun_Init_CreateEntriesDatabaseFails_SavesNothing(t *testing.T) {
	const oldTasksDataSourceID = "old-tasks-ds"

	tests := []struct {
		name     string
		response notionResponse
		wantExit int
		wantCode clierr.ErrorCode
	}{
		{
			// A wrong ID, or a page not shared with the integration
			name:     "parent_page_not_found",
			response: notionResponse{http.StatusNotFound, `{"object": "error", "status": 404, "code": "object_not_found", "message": "Could not find page."}`},
			wantExit: 3,
			wantCode: clierr.NotFound,
		},
		{
			name:     "rejected",
			response: notionResponse{http.StatusBadRequest, `{"object": "error", "status": 400, "code": "validation_error", "message": "body failed validation"}`},
			wantExit: 2,
			wantCode: clierr.Usage,
		},
		{
			name:     "server_error",
			response: notionResponse{http.StatusInternalServerError, `{"object": "error", "status": 500, "code": "internal_server_error", "message": "Unexpected error occurred."}`},
			wantExit: 10,
			wantCode: clierr.Internal,
		},
		{
			name:     "no_data_source_in_response",
			response: notionResponse{http.StatusOK, `{"object": "database", "id": "entries-db", "data_sources": []}`},
			wantExit: 10,
			wantCode: clierr.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			responses := connectedResponses(fullTasksSchema())
			responses[createDatabasePath] = tc.response
			s, _ := fakeNotion(t, responses)

			dbPath := filepath.Join(t.TempDir(), "test.db")
			seedDataSources(t, dbPath, oldTasksDataSourceID, mapProjectsDataSourceID)

			exit, stdout, stderr := runInitOnDB(t, dbPath, s.URL, s.Client(), withParentPage("--tasks-db", tasksDatabaseID)...)

			checkInitError(t, exit, stdout, stderr, tc.wantExit, tc.wantCode)

			checkEntriesDataSource(t, dbPath, "")
			gotTasks, _ := storedDataSources(t, dbPath)
			if gotTasks != oldTasksDataSourceID {
				t.Errorf("stored tasks data source = %q, want %q", gotTasks, oldTasksDataSourceID)
			}
			if got := storedContext(t, dbPath).PropertyMap; got != nil {
				t.Errorf("stored property map = %v, want none", got)
			}
		})
	}
}
