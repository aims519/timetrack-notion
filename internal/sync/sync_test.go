package sync

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/gofrs/flock"
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

func TestRun_LockFree_ReleasesLock(t *testing.T) {
	st, lockPath := openSyncStore(t, true)

	if err := Run(t.Context(), st, &fakeEntryManager{}, runClock(0), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("Run(%q) = %v, want nil", lockPath, err)
	}

	// Run must release the lock before returning, so another sync can take it
	other := flock.New(lockPath)
	locked, err := other.TryLock()
	if err != nil {
		t.Fatalf("TryLock after Run: %v", err)
	}
	if !locked {
		t.Errorf("lock still held after Run returned")
	}
	_ = other.Unlock()
}

func TestRun_Errors(t *testing.T) {
	tests := []struct {
		name      string
		connected bool
		lockPath  func(t *testing.T, defaultPath string) string
		wantCode  clierr.ErrorCode
	}{
		{
			// A held lock fails before any entry is read (§6.5)
			name:      "lock_held",
			connected: true,
			lockPath: func(t *testing.T, defaultPath string) string {
				holder := flock.New(defaultPath)
				locked, err := holder.TryLock()
				if err != nil || !locked {
					t.Fatalf("unable to hold lock for test: locked=%v, err=%v", locked, err)
				}
				t.Cleanup(func() { _ = holder.Unlock() })

				return defaultPath
			},
			wantCode: clierr.SyncBusy,
		},
		{
			name:      "directory_missing",
			connected: true,
			lockPath: func(t *testing.T, _ string) string {
				return filepath.Join(t.TempDir(), "missing", "tt.db.lock")
			},
			wantCode: clierr.Internal,
		},
		{
			name:      "not_initialised",
			connected: false,
			lockPath:  func(t *testing.T, defaultPath string) string { return defaultPath },
			wantCode:  clierr.NotInitialised,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, defaultPath := openSyncStore(t, tc.connected)
			entry := seedStoppedEntry(t, st, nil, testStart, testEnd)
			path := tc.lockPath(t, defaultPath)
			m := &fakeEntryManager{createdPageID: "page-1"}

			err := Run(t.Context(), st, m, runClock(0), path, testRetrySettings())

			checkCode(t, err, tc.wantCode)
			if n := m.requests(); n != 0 {
				t.Errorf("Run() made %d Notion requests, want 0", n)
			}
			checkStored(t, st, entry.ID, "pending", "")
		})
	}
}

func TestRun_CreatesPageForNewEntry(t *testing.T) {
	st, lockPath := openSyncStore(t, true)
	description := "pairing on sync"
	entry := seedStoppedEntry(t, st, &description, testStart, testEnd)
	m := &fakeEntryManager{createdPageID: "page-1"}

	if err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}

	// Looked up by Local ID first, in case an earlier create succeeded unrecorded (§6.2)
	if len(m.finds) != 1 || m.finds[0] != entry.ID {
		t.Errorf("FindEntryPage local IDs = %v, want [%s]", m.finds, entry.ID)
	}
	if len(m.updates) != 0 {
		t.Errorf("UpdateEntryPage called %d times, want 0", len(m.updates))
	}
	if len(m.creates) != 1 {
		t.Fatalf("CreateEntryPage called %d times, want 1", len(m.creates))
	}

	got := m.creates[0]
	// The title has its own test, TestRun_EntryPageTitle
	got.title = ""
	// Times are passed at full precision: truncating to the minute is the client's job
	want := entryPageCall{
		dataSourceID: testEntriesDataSourceID,
		description:  description,
		localID:      entry.ID,
		taskPageID:   testTaskID,
		start:        testStart,
		end:          testEnd,
	}
	if !got.start.Equal(want.start) || !got.end.Equal(want.end) {
		t.Errorf("CreateEntryPage start, end = %v, %v, want %v, %v", got.start, got.end, want.start, want.end)
	}
	got.start, got.end = want.start, want.end
	if got != want {
		t.Errorf("CreateEntryPage(%+v), want (%+v)", got, want)
	}

	checkStored(t, st, entry.ID, "synced", "page-1")
}

// Title is the description, else the task name (§5)
func TestRun_EntryPageTitle(t *testing.T) {
	tests := []struct {
		name        string
		description *string
		wantTitle   string
		wantDesc    string
	}{
		{name: "description", description: new("pairing on sync"), wantTitle: "pairing on sync", wantDesc: "pairing on sync"},
		{name: "no_description", description: nil, wantTitle: testTaskName, wantDesc: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			seedStoppedEntry(t, st, tc.description, testStart, testEnd)
			m := &fakeEntryManager{createdPageID: "page-1"}

			if err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings()); err != nil {
				t.Fatalf("Run() = %v, want nil", err)
			}
			if len(m.creates) != 1 {
				t.Fatalf("CreateEntryPage called %d times, want 1", len(m.creates))
			}
			if got := m.creates[0].title; got != tc.wantTitle {
				t.Errorf("title = %q, want %q", got, tc.wantTitle)
			}
			if got := m.creates[0].description; got != tc.wantDesc {
				t.Errorf("description = %q, want %q", got, tc.wantDesc)
			}
		})
	}
}

// A create that succeeded but was never recorded is found by Local ID and updated, not
// duplicated (§6.2, §10)
func TestRun_AdoptsPageFoundByLocalID(t *testing.T) {
	st, lockPath := openSyncStore(t, true)
	entry := seedStoppedEntry(t, st, nil, testStart, testEnd)
	m := &fakeEntryManager{foundPages: map[string]notion.Page{entry.ID: {ID: "page-existing"}}}

	if err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}

	if len(m.creates) != 0 {
		t.Errorf("CreateEntryPage called %d times, want 0", len(m.creates))
	}
	if len(m.updates) != 1 {
		t.Fatalf("UpdateEntryPage called %d times, want 1", len(m.updates))
	}
	if got := m.updates[0].pageID; got != "page-existing" {
		t.Errorf("UpdateEntryPage page ID = %q, want %q", got, "page-existing")
	}

	checkStored(t, st, entry.ID, "synced", "page-existing")
}

// An entry with a stored page ID is updated directly, without a lookup (§6.2). The lookup
// can't see trashed rows, so relying on it would recreate a row the user trashed (§6.3)
func TestRun_EntryWithPageID_UpdatesDirectly(t *testing.T) {
	st, lockPath := openSyncStore(t, true)
	entry := seedStoppedEntry(t, st, nil, testStart, testEnd)

	// First run records the page, then attaching the task again makes the entry pending
	first := &fakeEntryManager{createdPageID: "page-1"}
	if err := Run(t.Context(), st, first, runClock(0), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("first Run() = %v, want nil", err)
	}
	if _, err := st.AttachTaskToEntry(t.Context(), entry.ID, testTaskID, runClock(time.Minute).NowUTC()); err != nil {
		t.Fatalf("AttachTaskToEntry(): %v", err)
	}
	checkStored(t, st, entry.ID, "pending", "page-1")

	// The lookup finds nothing, as it would for a row the user has trashed
	m := &fakeEntryManager{createdPageID: "page-duplicate"}
	if err := Run(t.Context(), st, m, runClock(2*time.Minute), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("second Run() = %v, want nil", err)
	}

	if len(m.finds) != 0 {
		t.Errorf("FindEntryPage local IDs = %v, want none", m.finds)
	}
	if len(m.creates) != 0 {
		t.Errorf("CreateEntryPage called %d times, want 0", len(m.creates))
	}
	if len(m.updates) != 1 || m.updates[0].pageID != "page-1" {
		t.Errorf("UpdateEntryPage calls = %+v, want one for page-1", m.updates)
	}

	checkStored(t, st, entry.ID, "synced", "page-1")
}

// An entry edited while its create is in flight stays pending, and the next run finds the page
// by Local ID rather than creating a second one (§6.2, §6.3). The edit goes through the store
// during the request, so this also fails if a transaction is held across it (§4)
func TestRun_EditedDuringSync_StaysPending(t *testing.T) {
	st, lockPath := openSyncStore(t, true)
	entry := seedStoppedEntry(t, st, nil, testStart, testEnd)

	m := &fakeEntryManager{
		createdPageID: "page-1",
		onCreate: func(localID string) {
			if _, err := st.AttachTaskToEntry(t.Context(), localID, testTaskID, time.Now().UTC().Add(time.Second)); err != nil {
				t.Errorf("AttachTaskToEntry() during create: %v", err)
			}
		},
	}
	if err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("first Run() = %v, want nil", err)
	}
	checkStored(t, st, entry.ID, "pending", "")

	next := &fakeEntryManager{foundPages: map[string]notion.Page{entry.ID: {ID: "page-1"}}}
	if err := Run(t.Context(), st, next, runClock(time.Minute), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("second Run() = %v, want nil", err)
	}
	if len(next.creates) != 0 {
		t.Errorf("CreateEntryPage called %d times on the next run, want 0", len(next.creates))
	}
	checkStored(t, st, entry.ID, "synced", "page-1")
}

// A Notion error on one entry records its outcome and the run carries on with the next (§6.3)
func TestRun_NotionErrorOnOneEntry(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState string
	}{
		{name: "bad_request", err: &clierr.Error{Code: clierr.Usage, Message: "rejected request by Notion"}, wantState: "blocked"},
		{name: "not_found", err: &clierr.Error{Code: clierr.NotFound, Message: "object not found in Notion"}, wantState: "blocked"},
		{name: "server_error", err: serverError, wantState: "failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			bad := seedStoppedEntry(t, st, nil, testStart, testEnd)
			good := seedStoppedEntry(t, st, nil, testStart.Add(2*time.Hour), testEnd.Add(2*time.Hour))
			m := &fakeEntryManager{createdPageID: "page-good", createErr: map[string]error{bad.ID: tc.err}}

			if err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings()); err != nil {
				t.Fatalf("Run() = %v, want nil", err)
			}

			checkStored(t, st, bad.ID, tc.wantState, "")
			checkStored(t, st, good.ID, "synced", "page-good")
		})
	}
}

// An entry that failed last run is pushed again
func TestRun_RetriesFailedEntry(t *testing.T) {
	st, lockPath := openSyncStore(t, true)
	entry := seedStoppedEntry(t, st, nil, testStart, testEnd)

	failing := &fakeEntryManager{createErr: map[string]error{entry.ID: serverError}}
	if err := Run(t.Context(), st, failing, runClock(0), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("first Run() = %v, want nil", err)
	}
	checkStored(t, st, entry.ID, "failed", "")

	m := &fakeEntryManager{createdPageID: "page-1"}
	if err := Run(t.Context(), st, m, runClock(time.Minute), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("second Run() = %v, want nil", err)
	}

	checkStored(t, st, entry.ID, "synced", "page-1")
}

// A rejected token stops the run without recording a result, so the entry that hit it and every
// entry after it stay pending for the next run (§6.3)
func TestRun_AbortsRun(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode clierr.ErrorCode
	}{
		{name: "access_denied", err: &clierr.Error{Code: clierr.AccessDenied, Message: "invalid Notion API key"}, wantCode: clierr.AccessDenied},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			first := seedStoppedEntry(t, st, nil, testStart, testEnd)
			second := seedStoppedEntry(t, st, nil, testStart.Add(2*time.Hour), testEnd.Add(2*time.Hour))

			// Both entries fail, so the test doesn't depend on which one the run reaches first
			m := &fakeEntryManager{createErr: map[string]error{first.ID: tc.err, second.ID: tc.err}}

			err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings())

			checkCode(t, err, tc.wantCode)
			if len(m.creates) != 1 {
				t.Errorf("CreateEntryPage called %d times, want 1", len(m.creates))
			}
			checkStored(t, st, first.ID, "pending", "")
			checkStored(t, st, second.ID, "pending", "")
		})
	}
}

// serverError is the error the client returns for a 5xx
var serverError = &clierr.Error{Code: clierr.Internal, Message: "server error returned from Notion", Cause: notion.ErrServerError}

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

// A 429 is retried after waiting Retry-After, up to testRetrySettings().maxAttempts attempts in all, and only
// then is the entry marked failed. The run carries on with the next entry either way. Each attempt
// looks the entry up by Local ID again, as a create that seemed to fail may have succeeded (§6.2,
// §6.3)
func TestRun_RateLimited_Retries(t *testing.T) {
	tests := []struct {
		name         string
		createErr    error   // every create fails with this
		createErrSeq []error // or the first creates fail with these, then succeed
		wantAttempts int
		wantSleeps   []time.Duration
		wantState    string
		wantPageID   string
	}{
		{
			name:         "every_attempt",
			createErr:    rateLimited(2),
			wantAttempts: testRetrySettings().maxAttempts,
			// No wait after the last attempt: nothing follows it
			wantSleeps: slices.Repeat([]time.Duration{2 * time.Second}, testRetrySettings().maxAttempts-1),
			wantState:  "failed",
		},
		{
			name:         "once_then_success",
			createErrSeq: []error{rateLimited(2)},
			wantAttempts: 2,
			wantSleeps:   []time.Duration{2 * time.Second},
			wantState:    "synced",
			wantPageID:   "page-1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			limited := seedStoppedEntry(t, st, nil, testStart, testEnd)
			next := seedStoppedEntry(t, st, nil, testStart.Add(2*time.Hour), testEnd.Add(2*time.Hour))
			m := &fakeEntryManager{createdPageID: "page-1"}
			if tc.createErr != nil {
				m.createErr = map[string]error{limited.ID: tc.createErr}
			}
			if tc.createErrSeq != nil {
				m.createErrSeq = map[string][]error{limited.ID: tc.createErrSeq}
			}
			c := runClock(0)

			if err := Run(t.Context(), st, m, c, lockPath, testRetrySettings()); err != nil {
				t.Fatalf("Run() = %v, want nil", err)
			}

			if got := count(m.finds, limited.ID); got != tc.wantAttempts {
				t.Errorf("FindEntryPage called %d times for the limited entry, want %d", got, tc.wantAttempts)
			}
			var creates []string
			for _, call := range m.creates {
				creates = append(creates, call.localID)
			}
			if got := count(creates, limited.ID); got != tc.wantAttempts {
				t.Errorf("CreateEntryPage called %d times for the limited entry, want %d", got, tc.wantAttempts)
			}
			if !slices.Equal(c.sleeps, tc.wantSleeps) {
				t.Errorf("waits = %v, want %v", c.sleeps, tc.wantSleeps)
			}

			checkStored(t, st, limited.ID, tc.wantState, tc.wantPageID)
			checkStored(t, st, next.ID, "synced", "page-1")
		})
	}
}

// A 429 with no Retry-After waits a random time below a bound that doubles with each attempt, up
// to the cap ("full jitter"). The seed is fixed, so the waits repeat, but they're only checked
// against their bounds: strictly below, so a wait equal to its bound means the jitter is missing
func TestRun_RateLimited_Backoff(t *testing.T) {
	noRetryAfter := &clierr.Error{
		Code:    clierr.Offline,
		Message: "too many requests to Notion",
		Cause:   &notion.TooManyRequestsError{},
	}

	tests := []struct {
		name       string
		cap        time.Duration
		wantBounds []time.Duration // one per wait, the bound for the attempt before it
	}{
		{name: "doubling", cap: 30 * time.Second, wantBounds: []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}},
		{name: "capped", cap: 3 * time.Second, wantBounds: []time.Duration{1 * time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			limited := seedStoppedEntry(t, st, nil, testStart, testEnd)
			m := &fakeEntryManager{createErr: map[string]error{limited.ID: noRetryAfter}}
			c := runClock(0)
			settings := testRetrySettings()
			settings.cap = tc.cap

			if err := Run(t.Context(), st, m, c, lockPath, settings); err != nil {
				t.Fatalf("Run() = %v, want nil", err)
			}

			if len(m.creates) != settings.maxAttempts {
				t.Errorf("CreateEntryPage called %d times, want %d", len(m.creates), settings.maxAttempts)
			}
			if len(c.sleeps) != len(tc.wantBounds) {
				t.Fatalf("waits = %v, want %d of them", c.sleeps, len(tc.wantBounds))
			}
			for i, wait := range c.sleeps {
				if wait < 0 || wait >= tc.wantBounds[i] {
					t.Errorf("wait %d = %v, want at least 0 and below %v", i+1, wait, tc.wantBounds[i])
				}
			}
			checkStored(t, st, limited.ID, "failed", "")
		})
	}
}

// A 5xx is retried with full-jitter backoff, as Notion sends no Retry-After for it, and the entry
// is marked failed once the attempts are spent (§6.3). Other internal errors, such as a response
// that can't be parsed, would fail the same way again, so they aren't retried
func TestRun_ServerError_Retries(t *testing.T) {
	unparseable := &clierr.Error{Code: clierr.Internal, Message: "unable to parse Notion response"}

	tests := []struct {
		name         string
		createErr    error   // every create fails with this
		createErrSeq []error // or the first creates fail with these, then succeed
		wantAttempts int
		wantBounds   []time.Duration // one per wait
		wantState    string
		wantPageID   string
	}{
		{
			name:         "every_attempt",
			createErr:    serverError,
			wantAttempts: testRetrySettings().maxAttempts,
			wantBounds:   []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second},
			wantState:    "failed",
		},
		{
			name:         "once_then_success",
			createErrSeq: []error{serverError},
			wantAttempts: 2,
			wantBounds:   []time.Duration{1 * time.Second},
			wantState:    "synced",
			wantPageID:   "page-1",
		},
		{name: "not_retryable", createErr: unparseable, wantAttempts: 1, wantState: "failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			erroring := seedStoppedEntry(t, st, nil, testStart, testEnd)
			next := seedStoppedEntry(t, st, nil, testStart.Add(2*time.Hour), testEnd.Add(2*time.Hour))
			m := &fakeEntryManager{createdPageID: "page-1"}
			if tc.createErr != nil {
				m.createErr = map[string]error{erroring.ID: tc.createErr}
			}
			if tc.createErrSeq != nil {
				m.createErrSeq = map[string][]error{erroring.ID: tc.createErrSeq}
			}
			c := runClock(0)

			if err := Run(t.Context(), st, m, c, lockPath, testRetrySettings()); err != nil {
				t.Fatalf("Run() = %v, want nil", err)
			}

			if got := count(m.finds, erroring.ID); got != tc.wantAttempts {
				t.Errorf("FindEntryPage called %d times for the erroring entry, want %d", got, tc.wantAttempts)
			}
			var creates []string
			for _, call := range m.creates {
				creates = append(creates, call.localID)
			}
			if got := count(creates, erroring.ID); got != tc.wantAttempts {
				t.Errorf("CreateEntryPage called %d times for the erroring entry, want %d", got, tc.wantAttempts)
			}
			if len(c.sleeps) != len(tc.wantBounds) {
				t.Fatalf("waits = %v, want %d of them", c.sleeps, len(tc.wantBounds))
			}
			for i, wait := range c.sleeps {
				if wait < 0 || wait >= tc.wantBounds[i] {
					t.Errorf("wait %d = %v, want at least 0 and below %v", i+1, wait, tc.wantBounds[i])
				}
			}

			checkStored(t, st, erroring.ID, tc.wantState, tc.wantPageID)
			checkStored(t, st, next.ID, "synced", "page-1")
		})
	}
}

// Ctrl-C during a wait stops the run with cancelled. Nothing is recorded, so the entry that was
// waiting and every entry after it stay pending (§6.3)
func TestRun_RateLimited_CancelledDuringWait(t *testing.T) {
	st, lockPath := openSyncStore(t, true)
	limited := seedStoppedEntry(t, st, nil, testStart, testEnd)
	next := seedStoppedEntry(t, st, nil, testStart.Add(2*time.Hour), testEnd.Add(2*time.Hour))
	m := &fakeEntryManager{createdPageID: "page-1", createErr: map[string]error{limited.ID: rateLimited(2)}}
	c := runClock(0)
	c.sleepErr = context.Canceled

	err := Run(t.Context(), st, m, c, lockPath, testRetrySettings())

	checkCode(t, err, clierr.Cancelled)
	if len(m.creates) != 1 {
		t.Errorf("CreateEntryPage called %d times, want 1", len(m.creates))
	}
	checkStored(t, st, limited.ID, "pending", "")
	checkStored(t, st, next.ID, "pending", "")
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

// The entries data source is checked once, before any entry, and a failure stops the run with
// every entry left as it was (§6.3)
func TestRun_DataSourceCheck(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		inTrash      bool
		wantCode     clierr.ErrorCode // empty when the check passes
		wantUnusable bool             // wraps ErrDataSourceUnusable, so a re-check mid-run also stops
	}{
		{name: "found"},
		// A trashed database still answers 200, so entries would be written into the trash (decision 25)
		{name: "in_trash", inTrash: true, wantCode: clierr.NotFound, wantUnusable: true},
		{name: "not_found", err: &clierr.Error{Code: clierr.NotFound, Message: "object not found in Notion"}, wantCode: clierr.NotFound, wantUnusable: true},
		{name: "access_denied", err: &clierr.Error{Code: clierr.AccessDenied, Message: "invalid Notion API key"}, wantCode: clierr.AccessDenied},
		{name: "offline", err: &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion"}, wantCode: clierr.Offline},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			entry := seedStoppedEntry(t, st, nil, testStart, testEnd)
			m := &fakeEntryManager{dataSourceErr: tc.err, dataSourceInTrash: tc.inTrash, createdPageID: "page-1"}

			err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings())

			if len(m.dataSources) != 1 || m.dataSources[0] != testEntriesDataSourceID {
				t.Errorf("GetDataSource IDs = %v, want [%s]", m.dataSources, testEntriesDataSourceID)
			}

			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("Run() = %v, want nil", err)
				}
				checkStored(t, st, entry.ID, "synced", "page-1")
				return
			}

			checkCode(t, err, tc.wantCode)
			if got := errors.Is(err, notion.ErrDataSourceUnusable); got != tc.wantUnusable {
				t.Errorf("errors.Is(err, ErrDataSourceUnusable) = %v, want %v", got, tc.wantUnusable)
			}
			if n := len(m.finds) + len(m.creates) + len(m.updates); n != 0 {
				t.Errorf("Run() made %d entry requests after a failed check, want 0", n)
			}
			checkStored(t, st, entry.ID, "pending", "")
		})
	}
}

// With nothing to sync, the run makes no Notion requests at all
func TestRun_NothingPending_NoRequests(t *testing.T) {
	st, lockPath := openSyncStore(t, true)

	// A running entry is held back, so there is nothing to push
	taskID := testTaskID
	if _, err := st.StartNewTimer(t.Context(), testStart, &taskID, nil); err != nil {
		t.Fatalf("StartNewTimer(): %v", err)
	}
	m := &fakeEntryManager{}

	if err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings()); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
	if n := m.requests(); n != 0 {
		t.Errorf("Run() made %d Notion requests, want 0", n)
	}
}

// A data source that can't be used stops the run at the next lookup, rather than blocking every
// entry in turn: one that disappears mid-run, or one whose query is rejected
func TestRun_DataSourceUnusableDuringRun(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode clierr.ErrorCode
	}{
		{name: "not_found", err: dataSourceNotFound, wantCode: clierr.NotFound},
		{name: "query_rejected", err: dataSourceRejected, wantCode: clierr.Usage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			first := seedStoppedEntry(t, st, nil, testStart, testEnd)
			second := seedStoppedEntry(t, st, nil, testStart.Add(2*time.Hour), testEnd.Add(2*time.Hour))
			m := &fakeEntryManager{findErr: map[string]error{first.ID: tc.err, second.ID: tc.err}}

			err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings())

			checkCode(t, err, tc.wantCode)
			if len(m.finds) != 1 {
				t.Errorf("FindEntryPage called %d times, want 1", len(m.finds))
			}
			if len(m.creates) != 0 {
				t.Errorf("CreateEntryPage called %d times, want 0", len(m.creates))
			}
			checkStored(t, st, first.ID, "pending", "")
			checkStored(t, st, second.ID, "pending", "")
		})
	}
}

// A 404 on a create or update could be the data source or the entry's own page, so the data source
// is checked again. If it's gone the run stops; if it's fine only that entry is blocked (§6.3)
func TestRun_PageNotFound_RechecksDataSource(t *testing.T) {
	pageNotFound := &clierr.Error{Code: clierr.NotFound, Message: "object not found in Notion"}

	tests := []struct {
		name           string
		onUpdate       bool // the entry has a page in Notion, so the update gets the 404
		recheckErr     error
		recheckInTrash bool
		wantCode       clierr.ErrorCode // empty when the run carries on
		wantFirstState string           // the first entry's state when the run carries on
		wantAttempts   int              // attempts at the first entry, 0 meaning 1
	}{
		{name: "create_data_source_gone", recheckErr: pageNotFound, wantCode: clierr.NotFound},
		{name: "create_data_source_trashed", recheckInTrash: true, wantCode: clierr.NotFound},
		{name: "create_data_source_fine", wantFirstState: "blocked"},
		{name: "update_data_source_gone", onUpdate: true, recheckErr: pageNotFound, wantCode: clierr.NotFound},
		{name: "update_data_source_trashed", onUpdate: true, recheckInTrash: true, wantCode: clierr.NotFound},
		{name: "update_data_source_fine", onUpdate: true, wantFirstState: "blocked"},
		{
			name:       "recheck_offline",
			recheckErr: &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion"},
			wantCode:   clierr.Offline,
		},
		{name: "recheck_access_denied", recheckErr: &clierr.Error{Code: clierr.AccessDenied, Message: "invalid Notion API key"}, wantCode: clierr.AccessDenied},
		{
			// The re-check couldn't tell, so the whole entry is retried, 404 and re-check included,
			// and then left failed for next run rather than blocked
			name:           "recheck_server_error",
			recheckErr:     serverError,
			wantFirstState: "failed",
			wantAttempts:   testRetrySettings().maxAttempts,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			first := seedStoppedEntry(t, st, nil, testStart, testEnd)
			second := seedStoppedEntry(t, st, nil, testStart.Add(2*time.Hour), testEnd.Add(2*time.Hour))

			m := &fakeEntryManager{
				createdPageID:  "page-second",
				recheckErr:     tc.recheckErr,
				recheckInTrash: tc.recheckInTrash,
			}
			if tc.onUpdate {
				// The lookup finds both pages, so each entry is updated rather than created
				m.foundPages = map[string]notion.Page{first.ID: {ID: "page-first"}, second.ID: {ID: "page-second"}}
				m.updateErr = map[string]error{first.ID: pageNotFound}
			} else {
				m.createErr = map[string]error{first.ID: pageNotFound}
			}

			err := Run(t.Context(), st, m, runClock(0), lockPath, testRetrySettings())

			attempts := max(tc.wantAttempts, 1)
			// Once at the start of the run, and once after each 404
			if want := 1 + attempts; len(m.dataSources) != want {
				t.Errorf("GetDataSource called %d times, want %d", len(m.dataSources), want)
			}
			var firstWrites int
			for _, call := range append(m.creates, m.updates...) {
				if call.localID == first.ID {
					firstWrites++
				}
			}
			if firstWrites != attempts {
				t.Errorf("made %d creates and updates for the first entry, want %d", firstWrites, attempts)
			}

			if tc.wantCode != "" {
				checkCode(t, err, tc.wantCode)
				if n := len(m.creates) + len(m.updates); n != 1 {
					t.Errorf("made %d creates and updates, want 1", n)
				}
				checkStored(t, st, first.ID, "pending", "")
				checkStored(t, st, second.ID, "pending", "")
				return
			}

			if err != nil {
				t.Fatalf("Run() = %v, want nil", err)
			}
			checkStored(t, st, first.ID, tc.wantFirstState, "")
			checkStored(t, st, second.ID, "synced", "page-second")
		})
	}
}

// A tombstoned entry trashes its page rather than updating it, and one with no page in Notion
// is simply dropped: nothing is created (§6.1). Nothing tombstones an entry in the store yet, so
// deleted_at is set on the entry passed in
func TestSyncPendingEntry_Tombstone(t *testing.T) {
	tests := []struct {
		name         string
		storedPageID string // empty for none
		foundPageID  string // the Local ID lookup's result, empty for none
		wantFinds    int
		wantTrashes  []string
		wantPageID   string // empty for none
	}{
		{name: "not_found", wantFinds: 1},
		{name: "found_by_local_id", foundPageID: "page-found", wantFinds: 1, wantTrashes: []string{"page-found"}, wantPageID: "page-found"},
		// A stored page ID skips the lookup, which can't see trashed rows (§6.2)
		{name: "stored_page_id", storedPageID: "page-1", wantFinds: 0, wantTrashes: []string{"page-1"}, wantPageID: "page-1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := openSyncStore(t, true)
			e := seedStoppedEntry(t, st, nil, testStart, testEnd)
			deletedAt := testEnd.Add(time.Hour)
			e.DeletedAt = &deletedAt
			if tc.storedPageID != "" {
				e.EntryPageID = &tc.storedPageID
			}
			m := &fakeEntryManager{createdPageID: "page-created"}
			if tc.foundPageID != "" {
				m.foundPages = map[string]notion.Page{e.ID: {ID: tc.foundPageID}}
			}
			c := runClock(0)

			got, err := syncPendingEntry(t.Context(), testEntriesDataSourceID, e, st, m, c, c.NowUTC())
			if err != nil {
				t.Fatalf("syncPendingEntry() error = %v, want nil", err)
			}

			if len(m.finds) != tc.wantFinds {
				t.Errorf("FindEntryPage called %d times, want %d", len(m.finds), tc.wantFinds)
			}
			if len(m.creates) != 0 {
				t.Errorf("CreateEntryPage called %d times, want 0", len(m.creates))
			}
			if len(m.updates) != 0 {
				t.Errorf("UpdateEntryPage called %d times, want 0", len(m.updates))
			}
			if !slices.Equal(m.trashes, tc.wantTrashes) {
				t.Errorf("TrashEntryPage page IDs = %v, want %v", m.trashes, tc.wantTrashes)
			}

			if got.Status != "synced" {
				t.Errorf("Status = %q, want %q", got.Status, "synced")
			}
			var gotPageID string
			if got.EntryPageID != nil {
				gotPageID = *got.EntryPageID
			}
			if gotPageID != tc.wantPageID {
				t.Errorf("EntryPageID = %q, want %q", gotPageID, tc.wantPageID)
			}
			if got.SyncedAt == nil || !got.SyncedAt.Equal(c.NowUTC()) {
				t.Errorf("SyncedAt = %v, want %v", got.SyncedAt, c.NowUTC())
			}
			if got.SyncError != nil {
				t.Errorf("SyncError = %q, want nil", *got.SyncError)
			}
		})
	}
}

// Notion rejects trashing an already-trashed page with a 400, so a trash that gets a 400
// retrieves the page: trashed is success, otherwise the 400 stands and the entry is blocked
// (§6.1, question 19). A failed retrieve can't tell whether the page is trashed, so its error is
// returned instead of the 400: an offline retrieve stops the run with the entry still pending,
// rather than blocking it for good (§6.3)
func TestTrashPage(t *testing.T) {
	badRequest := &clierr.Error{Code: clierr.Usage, Message: "rejected request by Notion"}

	tests := []struct {
		name          string
		trashErr      error
		retrieved     notion.Page // what EntryPage returns after a 400
		retrieveErr   error       // what EntryPage fails with after a 400
		wantRetrieves int
		wantCode      clierr.ErrorCode // empty when the trash counts as success
	}{
		{name: "trashed", wantRetrieves: 0},
		{name: "already_trashed", trashErr: badRequest, retrieved: notion.Page{ID: "page-1", InTrash: true}, wantRetrieves: 1},
		{name: "bad_request_not_trashed", trashErr: badRequest, retrieved: notion.Page{ID: "page-1"}, wantRetrieves: 1, wantCode: clierr.Usage},
		{
			name:          "retrieve_offline",
			trashErr:      badRequest,
			retrieveErr:   &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion"},
			wantRetrieves: 1,
			wantCode:      clierr.Offline,
		},
		{
			name:          "retrieve_server_error",
			trashErr:      badRequest,
			retrieveErr:   serverError,
			wantRetrieves: 1,
			wantCode:      clierr.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &fakeEntryManager{
				trashErr:     map[string]error{"page-1": tc.trashErr},
				entryPages:   map[string]notion.Page{"page-1": tc.retrieved},
				entryPageErr: map[string]error{"page-1": tc.retrieveErr},
			}

			err := trashPage(t.Context(), m, testEntriesDataSourceID, "page-1")

			if len(m.trashes) != 1 || m.trashes[0] != "page-1" {
				t.Errorf("TrashEntryPage page IDs = %v, want [page-1]", m.trashes)
			}
			if len(m.retrieves) != tc.wantRetrieves {
				t.Errorf("EntryPage called %d times, want %d", len(m.retrieves), tc.wantRetrieves)
			}
			for _, id := range m.retrieves {
				if id != "page-1" {
					t.Errorf("EntryPage page ID = %q, want %q", id, "page-1")
				}
			}

			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("trashPage() = %v, want nil", err)
				}
				return
			}
			checkCode(t, err, tc.wantCode)
		})
	}
}

// A 404 on a trash could be the data source or the entry's own page, so the data source is
// checked again. If it's unusable the error wraps ErrDataSourceUnusable and the run stops;
// if it's fine the 404 stands, so only that entry is blocked (§6.3)
func TestTrashPage_NotFound_RechecksDataSource(t *testing.T) {
	pageNotFound := &clierr.Error{Code: clierr.NotFound, Message: "object not found in Notion"}

	tests := []struct {
		name         string
		checkErr     error
		checkInTrash bool
		wantCode     clierr.ErrorCode
		wantUnusable bool // the run stops rather than blocking the entry
	}{
		{name: "data_source_gone", checkErr: pageNotFound, wantCode: clierr.NotFound, wantUnusable: true},
		{name: "data_source_trashed", checkInTrash: true, wantCode: clierr.NotFound, wantUnusable: true},
		{name: "data_source_fine", wantCode: clierr.NotFound},
		{name: "check_offline", checkErr: &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion"}, wantCode: clierr.Offline},
		{name: "check_access_denied", checkErr: &clierr.Error{Code: clierr.AccessDenied, Message: "invalid Notion API key"}, wantCode: clierr.AccessDenied},
		{name: "check_server_error", checkErr: serverError, wantCode: clierr.Internal},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// trashPage is called directly, so its re-check is the fake's first GetDataSource
			m := &fakeEntryManager{
				trashErr:          map[string]error{"page-1": pageNotFound},
				dataSourceErr:     tc.checkErr,
				dataSourceInTrash: tc.checkInTrash,
			}

			err := trashPage(t.Context(), m, testEntriesDataSourceID, "page-1")

			if len(m.trashes) != 1 || m.trashes[0] != "page-1" {
				t.Errorf("TrashEntryPage page IDs = %v, want [page-1]", m.trashes)
			}
			if len(m.dataSources) != 1 || m.dataSources[0] != testEntriesDataSourceID {
				t.Errorf("GetDataSource IDs = %v, want [%s]", m.dataSources, testEntriesDataSourceID)
			}
			// Only a 400 retrieves the page
			if len(m.retrieves) != 0 {
				t.Errorf("EntryPage called %d times, want 0", len(m.retrieves))
			}

			checkCode(t, err, tc.wantCode)
			if got := errors.Is(err, notion.ErrDataSourceUnusable); got != tc.wantUnusable {
				t.Errorf("errors.Is(err, ErrDataSourceUnusable) = %v, want %v", got, tc.wantUnusable)
			}
		})
	}
}

func TestHandleSyncError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		wantState string           // when the run carries on
		wantCode  clierr.ErrorCode // when the run stops
	}{
		// Only reaches handleSyncError once its retries are spent, so it's recorded for next run
		{name: "rate_limited", err: rateLimited(30), wantState: "failed"},
		{name: "cancelled", err: fmt.Errorf("waiting to retry: %w", context.Canceled), wantCode: clierr.Cancelled},
		{name: "access_denied", err: &clierr.Error{Code: clierr.AccessDenied, Message: "invalid Notion API key"}, wantCode: clierr.AccessDenied},
		{name: "data_source_not_found", err: dataSourceNotFound, wantCode: clierr.NotFound},
		{name: "data_source_query_rejected", err: dataSourceRejected, wantCode: clierr.Usage},
		{name: "bad_request", err: &clierr.Error{Code: clierr.Usage, Message: "rejected request by Notion"}, wantState: "blocked"},
		{name: "not_found", err: &clierr.Error{Code: clierr.NotFound, Message: "object not found in Notion"}, wantState: "blocked"},
		{name: "server_error", err: serverError, wantState: "failed"},
		{name: "plain_error", err: errors.New("boom"), wantState: "failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := handleSyncError(tc.err)

			if tc.wantCode != "" {
				checkCode(t, err, tc.wantCode)
				return
			}

			if err != nil {
				t.Fatalf("handleSyncError() error = %v, want nil", err)
			}
			if got.Status != tc.wantState {
				t.Errorf("Status = %q, want %q", got.Status, tc.wantState)
			}
			if got.SyncError == nil || *got.SyncError == "" {
				t.Errorf("SyncError = %v, want a message", got.SyncError)
			}
			if got.SyncedAt != nil {
				t.Errorf("SyncedAt = %v, want nil", *got.SyncedAt)
			}
		})
	}
}
