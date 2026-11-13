package sync

import (
	"context"
	"errors"
	"math/rand/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/store"
)

const (
	testEntriesDataSourceID = "entries-ds"
	testTaskID              = "5555555555555555555555555e123456"
	testTaskName            = "Write tests"
)

var (
	testStart = time.Date(2026, 11, 10, 9, 0, 30, 0, time.UTC)
	testEnd   = time.Date(2026, 11, 10, 10, 15, 45, 0, time.UTC)
)

// fakeClock satisfies the clock interface Run needs, for testing. Sleep records the wait and
// returns at once, with sleepErr standing in for a context cancelled during the wait
type fakeClock struct {
	nowUTC   time.Time
	sleepErr error
	sleeps   []time.Duration
}

func (fc *fakeClock) NowUTC() time.Time {
	return fc.nowUTC
}

func (fc *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	fc.sleeps = append(fc.sleeps, d)
	return fc.sleepErr
}

// runClock is a clock for one sync run, offset to order several runs. It's fixed in the past,
// earlier than the wall-clock updated_at the store stamps when seeding, because the result of a
// run must not depend on how its clock compares with an entry's updated_at
func runClock(offset time.Duration) *fakeClock {
	return &fakeClock{nowUTC: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(offset)}
}

// testRetrySettings are the default retry settings, seeded so any jitter repeats from run to run
func testRetrySettings() RetrySettings {
	return DefaultRetrySettings(rand.NewPCG(1, 2))
}

var _ notionEntryManager = (*fakeEntryManager)(nil)

// entryPageCall records the arguments of one create or update request
type entryPageCall struct {
	pageID       string // update only
	dataSourceID string
	title        string
	description  string
	localID      string
	taskPageID   string
	start, end   time.Time
}

// fakeEntryManager answers lookups from foundPages and creates pages with createdPageID. Errors
// are keyed by local ID, so one entry can fail while another succeeds
type fakeEntryManager struct {
	dataSourceErr     error                  // returned by GetDataSource
	dataSourceInTrash bool                   // GetDataSource reports the data source as trashed
	recheckErr        error                  // returned by every GetDataSource after the first
	recheckInTrash    bool                   // every GetDataSource after the first reports it trashed
	dataSourceErrSeq  []error                // one error per check, in order, before the fields above
	foundPages        map[string]notion.Page // FindEntryPage results, by local ID
	createdPageID     string
	findErr           map[string]error
	createErr         map[string]error
	createErrSeq      map[string][]error   // one error per create, in order, then createErr
	onCreate          func(localID string) // runs during each create, before it returns
	updateErr         map[string]error
	trashErr          map[string]error       // by page ID, as a trash request has no local ID
	entryPages        map[string]notion.Page // EntryPage results, by page ID
	entryPageErr      map[string]error       // returned by EntryPage, by page ID

	dataSources []string // data source IDs checked
	finds       []string // local IDs looked up
	creates     []entryPageCall
	updates     []entryPageCall
	trashes     []string // page IDs trashed
	retrieves   []string // page IDs retrieved
}

func (f *fakeEntryManager) GetDataSource(ctx context.Context, dataSourceID string) (notion.DataSource, error) {
	f.dataSources = append(f.dataSources, dataSourceID)
	if len(f.dataSourceErrSeq) > 0 {
		err := f.dataSourceErrSeq[0]
		f.dataSourceErrSeq = f.dataSourceErrSeq[1:]
		if err != nil {
			return notion.DataSource{}, err
		}
	}
	if len(f.dataSources) > 1 {
		if f.recheckErr != nil {
			return notion.DataSource{}, f.recheckErr
		}
		return notion.DataSource{ID: dataSourceID, InTrash: f.recheckInTrash}, nil
	}
	if f.dataSourceErr != nil {
		return notion.DataSource{}, f.dataSourceErr
	}

	return notion.DataSource{ID: dataSourceID, InTrash: f.dataSourceInTrash}, nil
}

func (f *fakeEntryManager) FindEntryPage(ctx context.Context, entriesDataSourceID, localIDKey, localIDValue string) (notion.Page, bool, error) {
	f.finds = append(f.finds, localIDValue)
	if err := f.findErr[localIDValue]; err != nil {
		return notion.Page{}, false, err
	}

	page, found := f.foundPages[localIDValue]
	return page, found, nil
}

func (f *fakeEntryManager) CreateEntryPage(ctx context.Context, dataSourceID, title, description, localID, taskPageID string, start, end time.Time) (notion.Page, error) {
	f.creates = append(f.creates, entryPageCall{dataSourceID: dataSourceID, title: title, description: description, localID: localID, taskPageID: taskPageID, start: start, end: end})
	if f.onCreate != nil {
		f.onCreate(localID)
	}
	if seq := f.createErrSeq[localID]; len(seq) > 0 {
		f.createErrSeq[localID] = seq[1:]
		if seq[0] != nil {
			return notion.Page{}, seq[0]
		}
	}
	if err := f.createErr[localID]; err != nil {
		return notion.Page{}, err
	}

	return notion.Page{ID: f.createdPageID}, nil
}

func (f *fakeEntryManager) UpdateEntryPage(ctx context.Context, pageID, dataSourceID, title, description, localID, taskPageID string, start, end time.Time) (notion.Page, error) {
	f.updates = append(f.updates, entryPageCall{pageID: pageID, dataSourceID: dataSourceID, title: title, description: description, localID: localID, taskPageID: taskPageID, start: start, end: end})
	if err := f.updateErr[localID]; err != nil {
		return notion.Page{}, err
	}

	return notion.Page{ID: pageID}, nil
}

func (f *fakeEntryManager) TrashEntryPage(ctx context.Context, pageID, dataSourceID string) (notion.Page, error) {
	f.trashes = append(f.trashes, pageID)
	if err := f.trashErr[pageID]; err != nil {
		return notion.Page{}, err
	}

	return notion.Page{ID: pageID, InTrash: true}, nil
}

func (f *fakeEntryManager) EntryPage(ctx context.Context, pageID, dataSourceID string) (notion.Page, error) {
	f.retrieves = append(f.retrieves, pageID)
	if err := f.entryPageErr[pageID]; err != nil {
		return notion.Page{}, err
	}

	return f.entryPages[pageID], nil
}

// requests counts every Notion request the fake has answered
func (f *fakeEntryManager) requests() int {
	return len(f.dataSources) + len(f.finds) + len(f.creates) + len(f.updates) + len(f.trashes) + len(f.retrieves)
}

// openSyncStore opens a store in a temp directory with one cached task. With connected set,
// the context has an entries data source, as after tt init
func openSyncStore(t *testing.T, connected bool) (*store.Store, string) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("unable to open database: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("error closing store %v", err)
		}
	})

	if connected {
		nc, err := st.NotionContext(t.Context(), store.DefaultContextID)
		if err != nil {
			t.Fatalf("NotionContext(): %v", err)
		}
		id := testEntriesDataSourceID
		nc.EntriesDataSourceID = &id
		if err := st.SaveNotionContext(t.Context(), nc); err != nil {
			t.Fatalf("SaveNotionContext(): %v", err)
		}
	}

	edited := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	task := store.Task{ContextID: store.DefaultContextID, PageID: testTaskID, Name: testTaskName, LastEditedTime: edited}
	if _, err := st.RefreshTasks(t.Context(), []store.Task{task}, edited, true); err != nil {
		t.Fatalf("RefreshTasks(): %v", err)
	}

	return st, dbPath + ".lock"
}

// seedStoppedEntry starts and stops a timer on the cached task, so it's ready to sync
func seedStoppedEntry(t *testing.T, st *store.Store, description *string, start, end time.Time) store.Entry {
	t.Helper()

	taskID := testTaskID
	if _, err := st.StartNewTimer(t.Context(), start, &taskID, description); err != nil {
		t.Fatalf("StartNewTimer(): %v", err)
	}
	entry, err := st.StopTimer(t.Context(), end)
	if err != nil {
		t.Fatalf("StopTimer(): %v", err)
	}

	return entry
}

// storedEntry reads an entry back through the status query, which returns every entry started
// in the window it's given
func storedEntry(t *testing.T, st *store.Store, id string) store.Entry {
	t.Helper()

	status, err := st.ReadStatus(t.Context(), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ReadStatus(): %v", err)
	}
	for _, e := range status.TodayEntries {
		if e.ID == id {
			return e
		}
	}

	t.Fatalf("entry %q not found", id)
	return store.Entry{}
}

// checkStored fails the test unless the entry has the wanted sync state and page ID, an empty
// page ID meaning none
func checkStored(t *testing.T, st *store.Store, id, wantState, wantPageID string) {
	t.Helper()

	got := storedEntry(t, st, id)
	if got.SyncState != wantState {
		t.Errorf("entry %s sync_state = %q, want %q", id, got.SyncState, wantState)
	}

	var gotPageID string
	if got.EntryPageID != nil {
		gotPageID = *got.EntryPageID
	}
	if gotPageID != wantPageID {
		t.Errorf("entry %s entry_page_id = %q, want %q", id, gotPageID, wantPageID)
	}
}

func checkCode(t *testing.T, err error, want clierr.ErrorCode) {
	t.Helper()

	var cliErr *clierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("error = %v, want *clierr.Error", err)
	}
	if cliErr.Code != want {
		t.Errorf("error code = %q, want %q", cliErr.Code, want)
	}
}

// serverError is the error the client returns for a 5xx
var serverError = &clierr.Error{Code: clierr.Internal, Message: "server error returned from Notion", Cause: notion.ErrServerError}

// networkFailure is the error the client returns when a request gets no response from Notion
var networkFailure = &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion", Cause: notion.ErrNetworkFailure}

// rateLimited is the error the client returns for a 429, with the Retry-After seconds Notion sent
func rateLimited(retryAfterSeconds int) error {
	return &clierr.Error{
		Code:    clierr.Offline,
		Message: "too many requests to Notion",
		Cause:   &notion.TooManyRequestsError{RetryAfterSeconds: &retryAfterSeconds},
	}
}

// count returns how many times id appears in ids
func count(ids []string, id string) int {
	n := 0
	for _, got := range ids {
		if got == id {
			n++
		}
	}
	return n
}

// Errors the client returns when a lookup's data source can't be used: gone (404), or
// rejecting the query (400), as when the Local ID property is missing
var (
	dataSourceNotFound = &clierr.Error{
		Code:    clierr.NotFound,
		Message: "saved entries data source not found in Notion, restore or re-share",
		Cause:   notion.ErrDataSourceUnusable,
	}
	dataSourceRejected = &clierr.Error{
		Code:    clierr.Usage,
		Message: "entry page data source query rejected by Notion",
		Cause:   notion.ErrDataSourceUnusable,
	}
)
