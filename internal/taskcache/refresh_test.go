package taskcache

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/store"
)

const testContextID = "00000000-0000-0000-0000-000000000000"

// fakeClock satisfies the clock interface Refresh needs, for testing
type fakeClock struct {
	nowUTC time.Time
}

func (fc fakeClock) NowUTC() time.Time {
	return fc.nowUTC
}

var _ notionPageFetcher = (*fakeNotionFetcher)(nil)

// Data source IDs passed to Refresh. The fake fetcher answers testProjectsDataSourceID with
// projectPages, and any other data source with pages
const (
	testTasksDataSourceID    = "1234"
	testProjectsDataSourceID = "5678"
)

// testPropertyMap is the property map init builds for a tasks database using the default names
var testPropertyMap = map[string]string{"project": "Project", "order": "Order", "done": "Done"}

// fakeNotionFetcher records the tasks query and the projects query separately, so a test can
// check each one whichever order Refresh makes them in
type fakeNotionFetcher struct {
	pages               []notion.Page // returned for the tasks data source
	sinceFilterUsed     *time.Time    // since of the last tasks query
	invokedTimes        int           // tasks queries
	dataSourceIDQueried string        // data source of the last tasks query

	projectPages         []notion.Page // returned for the projects data source
	projectsSinceUsed    *time.Time    // since of the last projects query
	projectsQueriedTimes int

	err error // returned from every query
}

func (fetcher *fakeNotionFetcher) QueryDataSource(ctx context.Context, dataSourceID string, since *time.Time) ([]notion.Page, error) {
	var recordedSince *time.Time
	if since != nil {
		s := *since
		recordedSince = &s
	}

	if dataSourceID == testProjectsDataSourceID {
		fetcher.projectsSinceUsed = recordedSince
		fetcher.projectsQueriedTimes++
		return fetcher.projectPages, fetcher.err
	}

	fetcher.sinceFilterUsed = recordedSince
	fetcher.invokedTimes++
	fetcher.dataSourceIDQueried = dataSourceID

	return fetcher.pages, fetcher.err
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()

	dir := t.TempDir()
	tempDbPath := filepath.Join(dir, "test.db")

	st, err := store.Open(t.Context(), tempDbPath)
	if err != nil {
		t.Fatalf("unable to open database: %v", err)
	}

	// Register cleanup once Open has succeeded
	t.Cleanup(func() {
		errClose := st.Close()
		if errClose != nil {
			t.Errorf("error closing store %v", errClose)
		}
	})

	return st
}

func TestRefresh_IncrementalRefreshFetcherError(t *testing.T) {
	c := fakeClock{nowUTC: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)}
	st := openTestStore(t)
	fetcher := fakeNotionFetcher{pages: []notion.Page{}, err: &clierr.Error{Code: clierr.Offline, Message: "notion unreachable"}}

	_, err := Refresh(t.Context(), c, st, &fetcher, testTasksDataSourceID, testProjectsDataSourceID, false, testPropertyMap)
	if err == nil {
		t.Fatalf("Refresh() error = nil, want clierr.Error")
	}
	var cliErr *clierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("got %v(%T), want clierr.Error", err, err)
	}

	if cliErr.Code != clierr.Offline {
		t.Errorf("got err.Code = %v, want %v", cliErr.Code, clierr.Offline)
	}

	if cliErr.Message != "notion unreachable" {
		t.Errorf("got err.Message = %q, want %q", cliErr.Message, "notion unreachable")
	}

	cacheState, err := st.CacheState(t.Context())
	if err != nil {
		t.Fatalf("unable to query cache_state: %v", err)
	}

	if cacheState.TasksRefreshFailedAt == nil {
		t.Errorf("cache_state task refreshed failed nil, want %v", c.nowUTC)
	} else if !c.nowUTC.Equal(*cacheState.TasksRefreshFailedAt) {
		t.Errorf("cache_state task refresh failed at %q, want %q", *cacheState.TasksRefreshFailedAt, c.nowUTC)
	}
}

func TestRefresh_FirstIncrementalRefresh(t *testing.T) {
	// on first call, no since time should be sent
	c := fakeClock{nowUTC: time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)}
	st := openTestStore(t)

	pages := make([]notion.Page, 2)
	pages[0] = notion.Page{ID: "page-1", LastEditedTimeUTC: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}
	pages[1] = notion.Page{ID: "page-2", LastEditedTimeUTC: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)}
	fetcher := fakeNotionFetcher{pages: pages}

	res, err := Refresh(t.Context(), c, st, &fetcher, testTasksDataSourceID, testProjectsDataSourceID, false, testPropertyMap)
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	if fetcher.invokedTimes != 1 {
		t.Errorf("fetcher invoked %d times, want 1", fetcher.invokedTimes)
	}

	if fetcher.projectsQueriedTimes != 1 {
		t.Errorf("projects data source queried %d times, want 1", fetcher.projectsQueriedTimes)
	}

	if fetcher.dataSourceIDQueried != testTasksDataSourceID {
		t.Errorf("data source %q queried, want %q", fetcher.dataSourceIDQueried, testTasksDataSourceID)
	}

	if !res.RefreshedAt.Equal(c.nowUTC) {
		t.Errorf("got %v, want %v", res.RefreshedAt, c.nowUTC)
	}

	if res.GoneCount != nil {
		t.Errorf("got gone count %v, want nil", res.GoneCount)
	}

	if res.FetchedCount != 2 {
		t.Errorf("got %d pages returned, want %d", res.FetchedCount, 2)
	}

	if fetcher.sinceFilterUsed != nil {
		t.Errorf("got since %q, want nil", fetcher.sinceFilterUsed)
	}

	cacheState, err := st.CacheState(t.Context())
	if err != nil {
		t.Fatalf("unable to query cache_state: %v", err)
	}

	if cacheState.TasksRefreshedAt == nil {
		t.Errorf("cache state task refresh is nil, want %q", c.nowUTC)
	} else if !c.nowUTC.Equal(*cacheState.TasksRefreshedAt) {
		t.Errorf("cache_state tasks refreshed at %q, want %q", *cacheState.TasksRefreshedAt, c.nowUTC)
	}

	if cacheState.TasksFullRefreshedAt != nil {
		t.Errorf("cache_state full tasks refreshed at %q, want nil", cacheState.TasksFullRefreshedAt)
	}

	if cacheState.TasksRefreshFailedAt != nil {
		t.Errorf("cache_state refresh failed at %q, want nil", cacheState.TasksRefreshFailedAt)
	}
}

func TestMapPageToTask(t *testing.T) {
	const (
		pageID      = "11111111-2222-3333-4444-555555555555"
		wantPageID  = "11111111222233334444555555555555"
		projectA    = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		wantProjA   = "aaaaaaaabbbbccccddddeeeeeeeeeeee"
		projectB    = "99999999-8888-7777-6666-555555555555"
		titleOneRun = `"Task": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Write tests"}]}`
		oneProject  = `"Project": {"id": "p1", "type": "relation", "relation": [{"id": "` + projectA + `"}], "has_more": false}`
	)

	tests := []struct {
		name        string
		properties  string // the JSON inside "properties": {...}
		wantName    string
		wantProject *string // nil means no project
	}{
		{
			name:        "title and one project",
			properties:  titleOneRun + "," + oneProject,
			wantName:    "Write tests",
			wantProject: ptr(wantProjA),
		},
		{
			name: "title in several pieces is joined",
			properties: `"Task": {"id": "title", "type": "title", "title": [
				{"type": "text", "plain_text": "Write "},
				{"type": "text", "plain_text": "tests", "annotations": {"bold": true}},
				{"type": "text", "plain_text": " for refresh"}
			]},` + oneProject,
			wantName:    "Write tests for refresh",
			wantProject: ptr(wantProjA),
		},
		{
			name:        "no project",
			properties:  titleOneRun + `,"Project": {"id": "p1", "type": "relation", "relation": [], "has_more": false}`,
			wantName:    "Write tests",
			wantProject: nil,
		},
		{
			name:        "several projects takes the first",
			properties:  titleOneRun + `,"Project": {"id": "p1", "type": "relation", "relation": [{"id": "` + projectA + `"}, {"id": "` + projectB + `"}], "has_more": false}`,
			wantName:    "Write tests",
			wantProject: ptr(wantProjA),
		},
		// Edge cases
		{
			name:        "untitled task",
			properties:  `"Task": {"id": "title", "type": "title", "title": []},` + oneProject,
			wantName:    "",
			wantProject: ptr(wantProjA),
		},
		{
			name:        "title property missing",
			properties:  oneProject,
			wantName:    "",
			wantProject: ptr(wantProjA),
		},
		{
			name:        "title property has the wrong type",
			properties:  `"Task": {"id": "t1", "type": "rich_text", "rich_text": [{"type": "text", "plain_text": "Not a title"}]},` + oneProject,
			wantName:    "",
			wantProject: ptr(wantProjA),
		},
		{
			name:        "project property missing",
			properties:  titleOneRun,
			wantName:    "Write tests",
			wantProject: nil,
		},
		{
			name:        "project property has the wrong type",
			properties:  titleOneRun + `,"Project": {"id": "p1", "type": "select", "select": {"name": "Tracker"}}`,
			wantName:    "Write tests",
			wantProject: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := decodePage(t, `{
				"object": "page",
				"id": "`+pageID+`",
				"created_time": "2026-11-02T09:00:00.000Z",
				"last_edited_time": "2026-11-02T09:00:00.000Z",
				"in_trash": false,
				"properties": {`+tc.properties+`}
			}`)

			got := mapPageToTask(p, testContextID, testPropertyMap)

			if got.ContextID != testContextID {
				t.Errorf("ContextID = %q, want %q", got.ContextID, testContextID)
			}
			if got.PageID != wantPageID {
				t.Errorf("PageID = %q, want %q (no dashes)", got.PageID, wantPageID)
			}
			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}

			switch {
			case tc.wantProject == nil && got.ProjectPageID != nil:
				t.Errorf("ProjectPageID = %q, want nil", *got.ProjectPageID)
			case tc.wantProject != nil && got.ProjectPageID == nil:
				t.Errorf("ProjectPageID = nil, want %q", *tc.wantProject)
			case tc.wantProject != nil && *got.ProjectPageID != *tc.wantProject:
				t.Errorf("ProjectPageID = %q, want %q", *got.ProjectPageID, *tc.wantProject)
			}
		})
	}
}

func TestMapPageToTask_OrderAndDone(t *testing.T) {
	const title = `"Task": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Write tests"}]}`

	// order and done build the "Order" number and "Done" checkbox properties from a JSON value
	order := func(v string) string { return `"Order": {"id": "o1", "type": "number", "number": ` + v + `}` }
	done := func(v string) string { return `"Done": {"id": "d1", "type": "checkbox", "checkbox": ` + v + `}` }

	tests := []struct {
		name       string
		properties string // the JSON inside "properties": {...}
		wantOrder  *float64
		wantDone   bool
	}{
		{
			name:       "order and done set",
			properties: title + "," + order("4.5") + "," + done("true"),
			wantOrder:  ptr(4.5),
			wantDone:   true,
		},
		{
			name:       "not done",
			properties: title + "," + order("4.5") + "," + done("false"),
			wantOrder:  ptr(4.5),
			wantDone:   false,
		},
		{
			name:       "order with no value",
			properties: title + "," + order("null") + "," + done("false"),
			wantOrder:  nil,
		},
		// Edge cases
		{
			name:       "order of zero isn't the same as no order",
			properties: title + "," + order("0") + "," + done("false"),
			wantOrder:  ptr(0.0),
		},
		{
			name:       "decimal order keeps its value",
			properties: title + "," + order("4.1") + "," + done("false"),
			wantOrder:  ptr(4.1),
		},
		{
			name:       "negative order",
			properties: title + "," + order("-1") + "," + done("false"),
			wantOrder:  ptr(-1.0),
		},
		{
			name:       "order property missing",
			properties: title + "," + done("true"),
			wantOrder:  nil,
			wantDone:   true,
		},
		{
			name:       "order property has the wrong type",
			properties: title + `,"Order": {"id": "o1", "type": "rich_text", "rich_text": [{"type": "text", "plain_text": "4.5"}]},` + done("true"),
			wantOrder:  nil,
			wantDone:   true,
		},
		{
			name:       "done property missing",
			properties: title + "," + order("4.5"),
			wantOrder:  ptr(4.5),
			wantDone:   false,
		},
		{
			name:       "done property has the wrong type",
			properties: title + "," + order("4.5") + `,"Done": {"id": "d1", "type": "status", "status": {"name": "Done"}}`,
			wantOrder:  ptr(4.5),
			wantDone:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := decodePage(t, `{
				"object": "page",
				"id": "11111111-2222-3333-4444-555555555555",
				"created_time": "2026-11-02T09:00:00.000Z",
				"last_edited_time": "2026-11-02T09:00:00.000Z",
				"in_trash": false,
				"properties": {`+tc.properties+`}
			}`)

			got := mapPageToTask(p, testContextID, testPropertyMap)

			switch {
			case tc.wantOrder == nil && got.SortOrder != nil:
				t.Errorf("SortOrder = %v, want nil", *got.SortOrder)
			case tc.wantOrder != nil && got.SortOrder == nil:
				t.Errorf("SortOrder = nil, want %v", *tc.wantOrder)
			case tc.wantOrder != nil && *got.SortOrder != *tc.wantOrder:
				t.Errorf("SortOrder = %v, want %v", *got.SortOrder, *tc.wantOrder)
			}

			if got.Done != tc.wantDone {
				t.Errorf("Done = %v, want %v", got.Done, tc.wantDone)
			}
		})
	}
}

// Properties are read by the names in the map, and the title is found by type whatever its name
func TestMapPageToTask_UsesPropertyMap(t *testing.T) {
	const (
		projectID     = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		wantProjectID = "aaaaaaaabbbbccccddddeeeeeeeeeeee"
		title         = `"Name": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Write tests"}]}`
	)
	// The same values under the default names and under other names
	properties := func(project, order, done string) string {
		return `"` + project + `": {"id": "p1", "type": "relation", "relation": [{"id": "` + projectID + `"}], "has_more": false},
			"` + order + `": {"id": "o1", "type": "number", "number": 4.5},
			"` + done + `": {"id": "d1", "type": "checkbox", "checkbox": true}`
	}
	renamed := map[string]string{"project": "Client work", "order": "Rank", "done": "Complete"}

	tests := []struct {
		name        string
		properties  string // the JSON inside "properties": {...}
		propertyMap map[string]string
		wantProject *string
		wantOrder   *float64
		wantDone    bool
	}{
		{
			name:        "default_names",
			properties:  properties("Project", "Order", "Done"),
			propertyMap: testPropertyMap,
			wantProject: ptr(wantProjectID),
			wantOrder:   ptr(4.5),
			wantDone:    true,
		},
		{
			name:        "other_names",
			properties:  properties("Client work", "Rank", "Complete"),
			propertyMap: renamed,
			wantProject: ptr(wantProjectID),
			wantOrder:   ptr(4.5),
			wantDone:    true,
		},
		{
			name:        "default_names_ignored_when_mapped_elsewhere",
			properties:  properties("Project", "Order", "Done"),
			propertyMap: renamed,
		},
		{
			// A database with no Order property has no order key in its map
			name:        "order_not_mapped",
			properties:  properties("Project", "Order", "Done"),
			propertyMap: map[string]string{"project": "Project", "done": "Done"},
			wantProject: ptr(wantProjectID),
			wantDone:    true,
		},
		{
			// Why callers must not refresh before init has stored a map: nothing is read, and
			// there's no error to show it
			name:       "nil_map",
			properties: properties("Project", "Order", "Done"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := decodePage(t, `{
				"object": "page",
				"id": "11111111-2222-3333-4444-555555555555",
				"created_time": "2026-11-02T09:00:00.000Z",
				"last_edited_time": "2026-11-02T09:00:00.000Z",
				"in_trash": false,
				"properties": {`+title+`,`+tc.properties+`}
			}`)

			got := mapPageToTask(p, testContextID, tc.propertyMap)

			if got.Name != "Write tests" {
				t.Errorf("Name = %q, want %q", got.Name, "Write tests")
			}

			switch {
			case tc.wantProject == nil && got.ProjectPageID != nil:
				t.Errorf("ProjectPageID = %q, want nil", *got.ProjectPageID)
			case tc.wantProject != nil && got.ProjectPageID == nil:
				t.Errorf("ProjectPageID = nil, want %q", *tc.wantProject)
			case tc.wantProject != nil && *got.ProjectPageID != *tc.wantProject:
				t.Errorf("ProjectPageID = %q, want %q", *got.ProjectPageID, *tc.wantProject)
			}

			switch {
			case tc.wantOrder == nil && got.SortOrder != nil:
				t.Errorf("SortOrder = %v, want nil", *got.SortOrder)
			case tc.wantOrder != nil && got.SortOrder == nil:
				t.Errorf("SortOrder = nil, want %v", *tc.wantOrder)
			case tc.wantOrder != nil && *got.SortOrder != *tc.wantOrder:
				t.Errorf("SortOrder = %v, want %v", *got.SortOrder, *tc.wantOrder)
			}

			if got.Done != tc.wantDone {
				t.Errorf("Done = %v, want %v", got.Done, tc.wantDone)
			}
		})
	}
}

// The map a caller passes reaches the mapping, whichever way the refresh is started
func TestRefresh_PassesPropertyMapThrough(t *testing.T) {
	propertyMap := map[string]string{"project": "Client work", "order": "Rank", "done": "Complete"}

	tests := []struct {
		name    string
		refresh func(t *testing.T, st *store.Store, fetcher *fakeNotionFetcher) (RefreshResult, error)
	}{
		{
			name: "refresh",
			refresh: func(t *testing.T, st *store.Store, fetcher *fakeNotionFetcher) (RefreshResult, error) {
				return Refresh(t.Context(), fakeClock{nowUTC: firstRefreshAt}, st, fetcher, testTasksDataSourceID, testProjectsDataSourceID, false, propertyMap)
			},
		},
		{
			name: "auto_refresh",
			refresh: func(t *testing.T, st *store.Store, fetcher *fakeNotionFetcher) (RefreshResult, error) {
				return AutoRefresh(t.Context(), fakeClock{nowUTC: firstRefreshAt}, st, fetcher, testTasksDataSourceID, testProjectsDataSourceID, propertyMap)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)
			fetcher := &fakeNotionFetcher{pages: []notion.Page{{
				ID:                "page-1",
				LastEditedTimeUTC: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC),
				Properties: map[string]notion.PageProperty{
					"Name":        {Type: "title", Title: []notion.RichText{{PlainText: "Write tests"}}},
					"Client work": {Type: "relation", Relation: []notion.Relation{{ID: "project-1"}}},
					"Rank":        {Type: "number", Number: ptr(4.5)},
					"Complete":    {Type: "checkbox", Checkbox: true},
				},
			}}}

			if _, err := tc.refresh(t, st, fetcher); err != nil {
				t.Fatalf("refresh error = %v", err)
			}

			tasks, _, err := st.ListTasks(t.Context(), store.NoLimit, true, nil)
			if err != nil {
				t.Fatalf("ListTasks() error = %v", err)
			}
			if len(tasks) != 1 {
				t.Fatalf("got %d tasks, want 1", len(tasks))
			}

			got := tasks[0]
			if got.ProjectPageID == nil || *got.ProjectPageID != "project1" {
				t.Errorf("ProjectPageID = %v, want %q", got.ProjectPageID, "project1")
			}
			if got.SortOrder == nil || *got.SortOrder != 4.5 {
				t.Errorf("SortOrder = %v, want 4.5", got.SortOrder)
			}
			if !got.Done {
				t.Errorf("Done = false, want true")
			}
		})
	}
}

func TestMapPageToTask_Times(t *testing.T) {
	// created_time and last_edited_time differ, so a mix-up between them is caught
	p := decodePage(t, `{
		"object": "page",
		"id": "11111111-2222-3333-4444-555555555555",
		"created_time": "2026-10-20T08:00:00.000Z",
		"last_edited_time": "2026-11-02T09:15:00.000Z",
		"in_trash": false,
		"properties": {"Task": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Write tests"}]}}
	}`)

	got := mapPageToTask(p, testContextID, testPropertyMap)

	wantEdited := time.Date(2026, 11, 2, 9, 15, 0, 0, time.UTC)
	if !got.LastEditedTime.Equal(wantEdited) {
		t.Errorf("LastEditedTime = %v, want %v", got.LastEditedTime, wantEdited)
	}
	if got.LastEditedTime.Location() != time.UTC {
		t.Errorf("LastEditedTime location = %v, want UTC", got.LastEditedTime.Location())
	}

	// A page Notion returned is present, so it's never gone. Refresh sets gone_at, not the mapping
	if got.GoneAt != nil {
		t.Errorf("GoneAt = %v, want nil", *got.GoneAt)
	}
}

func TestMapPageToProject(t *testing.T) {
	const (
		pageID     = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		wantPageID = "aaaaaaaabbbbccccddddeeeeeeeeeeee"
	)

	tests := []struct {
		name       string
		properties string // the JSON inside "properties": {...}
		wantName   string
	}{
		{
			name:       "title in one piece",
			properties: `"Project name": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Sabbatical"}]}`,
			wantName:   "Sabbatical",
		},
		{
			name: "title in several pieces is joined",
			properties: `"Project name": {"id": "title", "type": "title", "title": [
				{"type": "text", "plain_text": "Time "},
				{"type": "text", "plain_text": "tracker", "annotations": {"bold": true}},
				{"type": "text", "plain_text": " CLI"}
			]}`,
			wantName: "Time tracker CLI",
		},
		{
			name: "other properties are ignored",
			properties: `"Project name": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Sabbatical"}]},
				"Status": {"id": "s1", "type": "status", "status": {"name": "Active"}}`,
			wantName: "Sabbatical",
		},
		// Edge cases
		{
			name:       "untitled project",
			properties: `"Project name": {"id": "title", "type": "title", "title": []}`,
			wantName:   "",
		},
		{
			name:       "title property missing",
			properties: `"Status": {"id": "s1", "type": "status", "status": {"name": "Active"}}`,
			wantName:   "",
		},
		{
			name:       "title property has the wrong type",
			properties: `"Project name": {"id": "n1", "type": "rich_text", "rich_text": [{"type": "text", "plain_text": "Not a title"}]}`,
			wantName:   "",
		},
		{
			// The title is found by type, because the projects database can call it anything
			name:       "title found whatever its name",
			properties: `"Name": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Sabbatical"}]}`,
			wantName:   "Sabbatical",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := decodePage(t, `{
				"object": "page",
				"id": "`+pageID+`",
				"created_time": "2026-11-02T09:00:00.000Z",
				"last_edited_time": "2026-11-02T09:00:00.000Z",
				"in_trash": false,
				"properties": {`+tc.properties+`}
			}`)

			got := mapPageToProject(p, testContextID)

			if got.ContextID != testContextID {
				t.Errorf("ContextID = %q, want %q", got.ContextID, testContextID)
			}
			// Tasks store their project's ID without dashes, so the project's ID must match it
			// for task list to find the project
			if got.PageID != wantPageID {
				t.Errorf("PageID = %q, want %q (no dashes)", got.PageID, wantPageID)
			}
			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			// A page Notion returned is present, so it's never gone. RefreshProjects sets gone_at, not the mapping
			if got.GoneAt != nil {
				t.Errorf("GoneAt = %v, want nil", *got.GoneAt)
			}
		})
	}
}

// The ID a task stores for its project and the ID the project is cached under must be the same,
// or task list can't join them
func TestMapPageToProject_IDMatchesTaskProjectID(t *testing.T) {
	const projectID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	task := mapPageToTask(decodePage(t, `{
		"object": "page",
		"id": "11111111-2222-3333-4444-555555555555",
		"created_time": "2026-11-02T09:00:00.000Z",
		"last_edited_time": "2026-11-02T09:00:00.000Z",
		"in_trash": false,
		"properties": {"Project": {"id": "p1", "type": "relation", "relation": [{"id": "`+projectID+`"}], "has_more": false}}
	}`), testContextID, testPropertyMap)

	project := mapPageToProject(decodePage(t, `{
		"object": "page",
		"id": "`+projectID+`",
		"created_time": "2026-11-02T09:00:00.000Z",
		"last_edited_time": "2026-11-02T09:00:00.000Z",
		"in_trash": false,
		"properties": {}
	}`), testContextID)

	if task.ProjectPageID == nil {
		t.Fatal("task ProjectPageID = nil, want the project's ID")
	}
	if *task.ProjectPageID != project.PageID {
		t.Errorf("task ProjectPageID = %q, project PageID = %q, want them equal", *task.ProjectPageID, project.PageID)
	}
}

// decodePage decodes a page as Notion returns it, so the test also checks that
// notion.Page's JSON tags match the shapes of the properties
func decodePage(t *testing.T, s string) notion.Page {
	t.Helper()

	var p notion.Page
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("decoding page JSON: %v", err)
	}

	return p
}

func ptr[T any](v T) *T {
	return &v
}

// Times for tests that refresh twice. They're far enough apart that the overlap
// (taskRefreshOverlapMinutes) can't hide a mix-up between them
var (
	firstRefreshAt  = time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	secondRefreshAt = time.Date(2026, 11, 3, 9, 15, 0, 0, time.UTC)
)

// pagesWithIDs returns one page per ID, each with a distinct page ID so they're cached as separate tasks
func pagesWithIDs(ids ...string) []notion.Page {
	pages := make([]notion.Page, len(ids))
	for i, id := range ids {
		pages[i] = notion.Page{ID: id, LastEditedTimeUTC: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)}
	}
	return pages
}

// refreshOnce runs one Refresh at now. Tests pass a new fetcher for each call, so each call's
// arguments are recorded separately
func refreshOnce(t *testing.T, st *store.Store, now time.Time, fetcher *fakeNotionFetcher, full bool) (RefreshResult, error) {
	t.Helper()
	return Refresh(t.Context(), fakeClock{nowUTC: now}, st, fetcher, testTasksDataSourceID, testProjectsDataSourceID, full, testPropertyMap)
}

// checkTime fails the test unless got and want are both nil, or both set to the same instant
func checkTime(t *testing.T, field string, got, want *time.Time) {
	t.Helper()

	switch {
	case want == nil && got != nil:
		t.Errorf("%s = %v, want nil", field, *got)
	case want != nil && got == nil:
		t.Errorf("%s = nil, want %v", field, *want)
	case want != nil && !got.Equal(*want):
		t.Errorf("%s = %v, want %v", field, *got, *want)
	}
}

func TestRefresh_Since(t *testing.T) {
	overlapStart := firstRefreshAt.Add(-taskRefreshOverlapMinutes * time.Minute)

	tests := []struct {
		name       string
		firstFull  bool
		firstFails bool // the first refresh's fetch fails, so it records a failure rather than a refresh
		secondFull bool
		wantSince  *time.Time
	}{
		{
			name:      "incremental after incremental asks for edits since the last refresh, less the overlap",
			wantSince: &overlapStart,
		},
		{
			// A full refresh also sets tasks_refreshed_at, so the next incremental one starts from it
			name:      "incremental after full uses the full refresh time",
			firstFull: true,
			wantSince: &overlapStart,
		},
		{
			name:       "full after incremental asks for everything",
			secondFull: true,
			wantSince:  nil,
		},
		{
			name:       "full after full asks for everything",
			firstFull:  true,
			secondFull: true,
			wantSince:  nil,
		},
		// Edge cases
		{
			// A failed refresh isn't a refresh, so there's still nothing to start from
			name:       "incremental after only a failed refresh asks for everything",
			firstFails: true,
			wantSince:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)

			first := &fakeNotionFetcher{pages: pagesWithIDs("page-1")}
			if tc.firstFails {
				first.err = &clierr.Error{Code: clierr.Offline, Message: "notion unreachable"}
			}
			_, err := refreshOnce(t, st, firstRefreshAt, first, tc.firstFull)
			if tc.firstFails != (err != nil) {
				t.Fatalf("first Refresh() error = %v, want an error: %v", err, tc.firstFails)
			}

			second := &fakeNotionFetcher{pages: pagesWithIDs("page-1")}
			if _, err := refreshOnce(t, st, secondRefreshAt, second, tc.secondFull); err != nil {
				t.Fatalf("second Refresh() error = %v", err)
			}

			if second.invokedTimes != 1 {
				t.Fatalf("second fetcher invoked %d times, want 1", second.invokedTimes)
			}
			checkTime(t, "since", second.sinceFilterUsed, tc.wantSince)

			// Projects are always fetched in full, so gone projects are found on every refresh
			if second.projectsQueriedTimes != 1 {
				t.Fatalf("second fetcher queried projects %d times, want 1", second.projectsQueriedTimes)
			}
			checkTime(t, "projects since", second.projectsSinceUsed, nil)
		})
	}
}

func TestRefresh_Full_CountsGoneTasks(t *testing.T) {
	tests := []struct {
		name        string
		cached      []string // page IDs cached by an earlier incremental refresh
		returned    []string // page IDs the full refresh gets back
		wantFetched int
		wantGone    int
	}{
		{"some tasks gone", []string{"page-1", "page-2", "page-3"}, []string{"page-1"}, 1, 2},
		// 0 gone is still a count, not nil, because a full refresh did check
		{"no tasks gone", []string{"page-1", "page-2"}, []string{"page-1", "page-2"}, 2, 0},
		{"new tasks aren't gone", []string{"page-1"}, []string{"page-1", "page-2", "page-3"}, 3, 0},
		// Edge cases
		{"nothing returned marks every task gone", []string{"page-1", "page-2"}, nil, 0, 2},
		{"nothing cached and nothing returned", nil, nil, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)

			if _, err := refreshOnce(t, st, firstRefreshAt, &fakeNotionFetcher{pages: pagesWithIDs(tc.cached...)}, false); err != nil {
				t.Fatalf("first Refresh() error = %v", err)
			}

			fetcher := &fakeNotionFetcher{pages: pagesWithIDs(tc.returned...)}
			res, err := refreshOnce(t, st, secondRefreshAt, fetcher, true)
			if err != nil {
				t.Fatalf("full Refresh() error = %v", err)
			}

			if fetcher.dataSourceIDQueried != testTasksDataSourceID {
				t.Errorf("data source %q queried, want %q", fetcher.dataSourceIDQueried, testTasksDataSourceID)
			}
			if res.FetchedCount != tc.wantFetched {
				t.Errorf("FetchedCount = %d, want %d", res.FetchedCount, tc.wantFetched)
			}
			if res.GoneCount == nil {
				t.Errorf("GoneCount = nil, want %d", tc.wantGone)
			} else if *res.GoneCount != tc.wantGone {
				t.Errorf("GoneCount = %d, want %d", *res.GoneCount, tc.wantGone)
			}
			checkTime(t, "RefreshedAt", &res.RefreshedAt, &secondRefreshAt)

			cacheState, err := st.CacheState(t.Context())
			if err != nil {
				t.Fatalf("CacheState() error = %v", err)
			}
			checkTime(t, "TasksRefreshedAt", cacheState.TasksRefreshedAt, &secondRefreshAt)
			checkTime(t, "TasksFullRefreshedAt", cacheState.TasksFullRefreshedAt, &secondRefreshAt)
			checkTime(t, "TasksRefreshFailedAt", cacheState.TasksRefreshFailedAt, nil)
		})
	}
}

func TestRefresh_FailureKeepsEarlierRefresh(t *testing.T) {
	tests := []struct {
		name       string
		secondFull bool
	}{
		{"incremental", false},
		{"full", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)

			if _, err := refreshOnce(t, st, firstRefreshAt, &fakeNotionFetcher{pages: pagesWithIDs("page-1")}, true); err != nil {
				t.Fatalf("first Refresh() error = %v", err)
			}

			fetchErr := &clierr.Error{Code: clierr.Offline, Message: "notion unreachable"}
			res, err := refreshOnce(t, st, secondRefreshAt, &fakeNotionFetcher{err: fetchErr}, tc.secondFull)
			if !errors.Is(err, fetchErr) {
				t.Fatalf("Refresh() error = %v, want the fetcher's error unchanged", err)
			}
			if res.GoneCount != nil {
				t.Errorf("GoneCount = %d alongside an error, want nil", *res.GoneCount)
			}

			// The earlier refresh times are kept, so the next incremental refresh doesn't skip the edits
			// this one missed. Only the failure is recorded
			cacheState, err := st.CacheState(t.Context())
			if err != nil {
				t.Fatalf("CacheState() error = %v", err)
			}
			checkTime(t, "TasksRefreshedAt", cacheState.TasksRefreshedAt, &firstRefreshAt)
			checkTime(t, "TasksFullRefreshedAt", cacheState.TasksFullRefreshedAt, &firstRefreshAt)
			checkTime(t, "TasksRefreshFailedAt", cacheState.TasksRefreshFailedAt, &secondRefreshAt)

			// A failed full refresh mustn't mark anything gone, so a later full refresh that gets
			// the task back finds nothing gone
			res, err = refreshOnce(t, st, secondRefreshAt.Add(time.Minute), &fakeNotionFetcher{pages: pagesWithIDs("page-1")}, true)
			if err != nil {
				t.Fatalf("third Refresh() error = %v", err)
			}
			if res.GoneCount == nil || *res.GoneCount != 0 {
				t.Errorf("GoneCount after the failed refresh = %v, want 0", res.GoneCount)
			}
		})
	}
}

// cancellingFetcher cancels the refresh's context while the fetch is in progress, as Ctrl-C
// would, then fails as the cancelled request would
type cancellingFetcher struct {
	cancel context.CancelFunc
}

func (f *cancellingFetcher) QueryDataSource(ctx context.Context, dataSourceID string, since *time.Time) ([]notion.Page, error) {
	f.cancel()
	return nil, ctx.Err()
}

func TestRefresh_CancelledFetchDoesNotRecordFailure(t *testing.T) {
	st := openTestStore(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, err := Refresh(ctx, fakeClock{nowUTC: firstRefreshAt}, st, &cancellingFetcher{cancel: cancel}, testTasksDataSourceID, testProjectsDataSourceID, false, testPropertyMap)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Refresh() error = %v, want context.Canceled", err)
	}

	// A cancellation says nothing about whether Notion is reachable, so it doesn't start the back-off
	cacheState, err := st.CacheState(t.Context())
	if err != nil {
		t.Fatalf("CacheState() error = %v", err)
	}
	checkTime(t, "TasksRefreshFailedAt", cacheState.TasksRefreshFailedAt, nil)
	checkTime(t, "TasksRefreshedAt", cacheState.TasksRefreshedAt, nil)
}

// slowFetcher blocks until the refresh's context ends, as a request to an unresponsive Notion
// would, then fails as that request would
type slowFetcher struct{}

func (f slowFetcher) QueryDataSource(ctx context.Context, dataSourceID string, since *time.Time) ([]notion.Page, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRefresh_TimedOutFetchRecordsFailure(t *testing.T) {
	st := openTestStore(t)

	// Long enough for Refresh to read the cache state, so the deadline expires during the fetch
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	_, err := Refresh(ctx, fakeClock{nowUTC: firstRefreshAt}, st, slowFetcher{}, testTasksDataSourceID, testProjectsDataSourceID, false, testPropertyMap)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Refresh() error = %v, want context.DeadlineExceeded", err)
	}

	// The failure is written after the deadline has passed, so this only passes because
	// Refresh writes it with a context that ignores the deadline
	cacheState, err := st.CacheState(t.Context())
	if err != nil {
		t.Fatalf("CacheState() error = %v", err)
	}
	checkTime(t, "TasksRefreshFailedAt", cacheState.TasksRefreshFailedAt, &firstRefreshAt)
	checkTime(t, "TasksRefreshedAt", cacheState.TasksRefreshedAt, nil)
}

func TestRefresh_CacheStateUnreadable_DoesNotFetch(t *testing.T) {
	st := openTestStore(t)

	// Every query on a closed store fails. Closing again in openTestStore's cleanup is harmless
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	fetcher := &fakeNotionFetcher{pages: pagesWithIDs("page-1")}
	_, err := refreshOnce(t, st, firstRefreshAt, fetcher, false)
	if err == nil {
		t.Fatal("Refresh() error = nil, want an error")
	}

	var cliErr *clierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("Refresh() error = %v (%T), want a *clierr.Error", err, err)
	}
	if cliErr.Code != clierr.DatabaseUnusable {
		t.Errorf("error code = %q, want %q", cliErr.Code, clierr.DatabaseUnusable)
	}

	// Without the cache state it can't work out what to ask for, so Notion isn't called at all
	if fetcher.invokedTimes != 0 {
		t.Errorf("fetcher invoked %d times, want 0", fetcher.invokedTimes)
	}
}

func autoRefreshOnce(t *testing.T, st *store.Store, now time.Time, fetcher notionPageFetcher) (RefreshResult, error) {
	t.Helper()
	return AutoRefresh(t.Context(), fakeClock{nowUTC: now}, st, fetcher, testTasksDataSourceID, testProjectsDataSourceID, testPropertyMap)
}

func TestAutoRefresh_DecidesWhetherToRefresh(t *testing.T) {
	now := secondRefreshAt

	// A zero duration means it has never happened
	tests := []struct {
		name            string
		refreshedAgo    time.Duration
		failedAgo       time.Duration
		wantRefresh     bool
		wantBackoffSkip bool
	}{
		{name: "never refreshed", wantRefresh: true},
		{name: "refreshed under 10 minutes ago", refreshedAgo: 10*time.Minute - time.Second, wantRefresh: false},
		{name: "refreshed exactly 10 minutes ago", refreshedAgo: 10 * time.Minute, wantRefresh: true},
		{name: "refreshed over 10 minutes ago", refreshedAgo: 10*time.Minute + time.Second, wantRefresh: true},
		{name: "never refreshed, failed under a minute ago", failedAgo: time.Minute - time.Second, wantRefresh: false, wantBackoffSkip: true},
		{name: "stale, failed under a minute ago", refreshedAgo: time.Hour, failedAgo: time.Minute - time.Second, wantRefresh: false, wantBackoffSkip: true},
		{name: "stale, failed exactly a minute ago", refreshedAgo: time.Hour, failedAgo: time.Minute, wantRefresh: true},
		{name: "stale, failed over a minute ago", refreshedAgo: time.Hour, failedAgo: time.Minute + time.Second, wantRefresh: true},
		{name: "fresh, failed over a minute ago", refreshedAgo: time.Minute, failedAgo: 5 * time.Minute, wantRefresh: false},
		// A fresh cache isn't stale, so a recent failure mustn't report a back-off skip
		{name: "fresh, failed under a minute ago", refreshedAgo: time.Minute, failedAgo: 30 * time.Second, wantRefresh: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t)

			var wantRefreshedAt, wantFailedAt *time.Time
			if tc.refreshedAgo != 0 {
				refreshedAt := now.Add(-tc.refreshedAgo)
				wantRefreshedAt = &refreshedAt
				if _, err := refreshOnce(t, st, refreshedAt, &fakeNotionFetcher{pages: pagesWithIDs("page-1")}, false); err != nil {
					t.Fatalf("seeding Refresh() error = %v", err)
				}
			}
			if tc.failedAgo != 0 {
				failedAt := now.Add(-tc.failedAgo)
				wantFailedAt = &failedAt
				if err := st.RecordTasksRefreshFailed(t.Context(), failedAt); err != nil {
					t.Fatalf("RecordTasksRefreshFailed() error = %v", err)
				}
			}

			fetcher := &fakeNotionFetcher{pages: pagesWithIDs("page-2")}
			res, err := autoRefreshOnce(t, st, now, fetcher)
			if err != nil {
				t.Fatalf("AutoRefresh() error = %v, want nil", err)
			}

			wantQueries, wantFetched := 0, 0
			var wantResultAt time.Time
			if tc.wantRefresh {
				wantQueries, wantFetched = 1, 1
				wantResultAt = now
				wantRefreshedAt = &now
			}

			if fetcher.invokedTimes != wantQueries {
				t.Errorf("tasks queried %d times, want %d", fetcher.invokedTimes, wantQueries)
			}
			if fetcher.projectsQueriedTimes != wantQueries {
				t.Errorf("projects queried %d times, want %d", fetcher.projectsQueriedTimes, wantQueries)
			}
			if res.FetchedCount != wantFetched {
				t.Errorf("FetchedCount = %d, want %d", res.FetchedCount, wantFetched)
			}
			if !res.RefreshedAt.Equal(wantResultAt) {
				t.Errorf("RefreshedAt = %v, want %v", res.RefreshedAt, wantResultAt)
			}
			if res.BackoffSkip != tc.wantBackoffSkip {
				t.Errorf("BackoffSkip = %v, want %v", res.BackoffSkip, tc.wantBackoffSkip)
			}

			// Skipping leaves the state alone. In particular it doesn't restart the back-off, which
			// would stop commands run less than a minute apart from ever refreshing
			cacheState, err := st.CacheState(t.Context())
			if err != nil {
				t.Fatalf("CacheState() error = %v", err)
			}
			checkTime(t, "TasksRefreshedAt", cacheState.TasksRefreshedAt, wantRefreshedAt)
			checkTime(t, "TasksRefreshFailedAt", cacheState.TasksRefreshFailedAt, wantFailedAt)
		})
	}
}

func TestAutoRefresh_IsIncremental(t *testing.T) {
	st := openTestStore(t)

	if _, err := refreshOnce(t, st, firstRefreshAt, &fakeNotionFetcher{pages: pagesWithIDs("page-1")}, true); err != nil {
		t.Fatalf("seeding Refresh() error = %v", err)
	}

	// page-1 isn't returned this time. An incremental refresh must not treat that as gone
	fetcher := &fakeNotionFetcher{pages: pagesWithIDs("page-2")}
	res, err := autoRefreshOnce(t, st, secondRefreshAt, fetcher)
	if err != nil {
		t.Fatalf("AutoRefresh() error = %v", err)
	}

	wantSince := firstRefreshAt.Add(-taskRefreshOverlapMinutes * time.Minute)
	checkTime(t, "tasks since", fetcher.sinceFilterUsed, &wantSince)
	if res.GoneCount != nil {
		t.Errorf("GoneCount = %d, want nil", *res.GoneCount)
	}

	cacheState, err := st.CacheState(t.Context())
	if err != nil {
		t.Fatalf("CacheState() error = %v", err)
	}
	checkTime(t, "TasksFullRefreshedAt", cacheState.TasksFullRefreshedAt, &firstRefreshAt)
}

// deadlineRecordingFetcher records the deadline of the context the refresh passes to Notion
type deadlineRecordingFetcher struct {
	deadline    time.Time
	hasDeadline bool
}

func (f *deadlineRecordingFetcher) QueryDataSource(ctx context.Context, dataSourceID string, since *time.Time) ([]notion.Page, error) {
	f.deadline, f.hasDeadline = ctx.Deadline()
	return nil, nil
}

func TestAutoRefresh_CapsRefreshAtTwoSeconds(t *testing.T) {
	st := openTestStore(t)
	fetcher := &deadlineRecordingFetcher{}

	before := time.Now()
	if _, err := autoRefreshOnce(t, st, firstRefreshAt, fetcher); err != nil {
		t.Fatalf("AutoRefresh() error = %v", err)
	}
	after := time.Now()

	if !fetcher.hasDeadline {
		t.Fatal("fetch context has no deadline, want one 2s after the refresh starts")
	}
	if fetcher.deadline.Before(before.Add(2*time.Second)) || fetcher.deadline.After(after.Add(2*time.Second)) {
		t.Errorf("deadline = %v, want between %v and %v", fetcher.deadline, before.Add(2*time.Second), after.Add(2*time.Second))
	}
}

func TestAutoRefresh_FailureBacksOffForAMinute(t *testing.T) {
	st := openTestStore(t)

	if _, err := refreshOnce(t, st, firstRefreshAt, &fakeNotionFetcher{pages: pagesWithIDs("page-1")}, false); err != nil {
		t.Fatalf("seeding Refresh() error = %v", err)
	}

	fetchErr := &clierr.Error{Code: clierr.Offline, Message: "notion unreachable"}
	failedAt := secondRefreshAt
	if _, err := autoRefreshOnce(t, st, failedAt, &fakeNotionFetcher{err: fetchErr}); !errors.Is(err, fetchErr) {
		t.Fatalf("AutoRefresh() error = %v, want the fetcher's error unchanged", err)
	}

	cacheState, err := st.CacheState(t.Context())
	if err != nil {
		t.Fatalf("CacheState() error = %v", err)
	}
	checkTime(t, "TasksRefreshFailedAt", cacheState.TasksRefreshFailedAt, &failedAt)
	checkTime(t, "TasksRefreshedAt", cacheState.TasksRefreshedAt, &firstRefreshAt)

	during := &fakeNotionFetcher{pages: pagesWithIDs("page-1")}
	if _, err := autoRefreshOnce(t, st, failedAt.Add(30*time.Second), during); err != nil {
		t.Fatalf("AutoRefresh() during back-off error = %v, want nil", err)
	}
	if during.invokedTimes != 0 {
		t.Errorf("fetcher invoked %d times during back-off, want 0", during.invokedTimes)
	}

	retriedAt := failedAt.Add(time.Minute)
	after := &fakeNotionFetcher{pages: pagesWithIDs("page-1")}
	if _, err := autoRefreshOnce(t, st, retriedAt, after); err != nil {
		t.Fatalf("AutoRefresh() after back-off error = %v", err)
	}
	if after.invokedTimes != 1 {
		t.Errorf("fetcher invoked %d times after back-off, want 1", after.invokedTimes)
	}

	cacheState, err = st.CacheState(t.Context())
	if err != nil {
		t.Fatalf("CacheState() error = %v", err)
	}
	checkTime(t, "TasksRefreshedAt", cacheState.TasksRefreshedAt, &retriedAt)
}

func TestAutoRefresh_CacheStateUnreadable_DoesNotFetch(t *testing.T) {
	st := openTestStore(t)

	// Every query on a closed store fails. Closing again in openTestStore's cleanup is harmless
	if err := st.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	fetcher := &fakeNotionFetcher{pages: pagesWithIDs("page-1")}
	_, err := autoRefreshOnce(t, st, firstRefreshAt, fetcher)

	var cliErr *clierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("AutoRefresh() error = %v (%T), want a *clierr.Error", err, err)
	}
	if cliErr.Code != clierr.DatabaseUnusable {
		t.Errorf("error code = %q, want %q", cliErr.Code, clierr.DatabaseUnusable)
	}
	if fetcher.invokedTimes != 0 {
		t.Errorf("fetcher invoked %d times, want 0", fetcher.invokedTimes)
	}
}
