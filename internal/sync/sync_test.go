package sync

import (
	"context"
	"errors"
	"path/filepath"
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

// fakeClock satisfies the clock interface Run needs, for testing
type fakeClock struct {
	nowUTC time.Time
}

func (fc fakeClock) NowUTC() time.Time {
	return fc.nowUTC
}

// runClock is a clock for one sync run, offset to order several runs. It's fixed in the past,
// earlier than the wall-clock updated_at the store stamps when seeding, because the result of a
// run must not depend on how its clock compares with an entry's updated_at
func runClock(offset time.Duration) fakeClock {
	return fakeClock{nowUTC: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).Add(offset)}
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
	foundPages    map[string]notion.Page // FindEntryPage results, by local ID
	createdPageID string
	findErr       map[string]error
	createErr     map[string]error
	onCreate      func(localID string) // runs during each create, before it returns
	updateErr     map[string]error

	finds   []string // local IDs looked up
	creates []entryPageCall
	updates []entryPageCall
	trashes []string
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
	return notion.Page{ID: pageID, InTrash: true}, nil
}

// requests counts every Notion request the fake has answered
func (f *fakeEntryManager) requests() int {
	return len(f.finds) + len(f.creates) + len(f.updates) + len(f.trashes)
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

	if err := Run(t.Context(), st, &fakeEntryManager{}, runClock(0), lockPath); err != nil {
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

			err := Run(t.Context(), st, m, runClock(0), path)

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

	if err := Run(t.Context(), st, m, runClock(0), lockPath); err != nil {
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

			if err := Run(t.Context(), st, m, runClock(0), lockPath); err != nil {
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

	if err := Run(t.Context(), st, m, runClock(0), lockPath); err != nil {
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
	if err := Run(t.Context(), st, first, runClock(0), lockPath); err != nil {
		t.Fatalf("first Run() = %v, want nil", err)
	}
	if _, err := st.AttachTaskToEntry(t.Context(), entry.ID, testTaskID, runClock(time.Minute).NowUTC()); err != nil {
		t.Fatalf("AttachTaskToEntry(): %v", err)
	}
	checkStored(t, st, entry.ID, "pending", "page-1")

	// The lookup finds nothing, as it would for a row the user has trashed
	m := &fakeEntryManager{createdPageID: "page-duplicate"}
	if err := Run(t.Context(), st, m, runClock(2*time.Minute), lockPath); err != nil {
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
	if err := Run(t.Context(), st, m, runClock(0), lockPath); err != nil {
		t.Fatalf("first Run() = %v, want nil", err)
	}
	checkStored(t, st, entry.ID, "pending", "")

	next := &fakeEntryManager{foundPages: map[string]notion.Page{entry.ID: {ID: "page-1"}}}
	if err := Run(t.Context(), st, next, runClock(time.Minute), lockPath); err != nil {
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
		{name: "server_error", err: &clierr.Error{Code: clierr.Internal, Message: "unexpected Notion error"}, wantState: "failed"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			bad := seedStoppedEntry(t, st, nil, testStart, testEnd)
			good := seedStoppedEntry(t, st, nil, testStart.Add(2*time.Hour), testEnd.Add(2*time.Hour))
			m := &fakeEntryManager{createdPageID: "page-good", createErr: map[string]error{bad.ID: tc.err}}

			if err := Run(t.Context(), st, m, runClock(0), lockPath); err != nil {
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

	failing := &fakeEntryManager{createErr: map[string]error{entry.ID: &clierr.Error{Code: clierr.Internal, Message: "unexpected Notion error"}}}
	if err := Run(t.Context(), st, failing, runClock(0), lockPath); err != nil {
		t.Fatalf("first Run() = %v, want nil", err)
	}
	checkStored(t, st, entry.ID, "failed", "")

	m := &fakeEntryManager{createdPageID: "page-1"}
	if err := Run(t.Context(), st, m, runClock(time.Minute), lockPath); err != nil {
		t.Fatalf("second Run() = %v, want nil", err)
	}

	checkStored(t, st, entry.ID, "synced", "page-1")
}

// A rate limit or a rejected token stops the run without recording a result, so the entry that
// hit it and every entry after it stay pending for the next run (§6.3)
func TestRun_AbortsRun(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode clierr.ErrorCode
	}{
		{
			name:     "rate_limited",
			err:      &clierr.Error{Code: clierr.Offline, Message: "too many requests to Notion", Cause: &notion.TooManyRequestsError{}},
			wantCode: clierr.Offline,
		},
		{name: "access_denied", err: &clierr.Error{Code: clierr.AccessDenied, Message: "invalid Notion API key"}, wantCode: clierr.AccessDenied},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			first := seedStoppedEntry(t, st, nil, testStart, testEnd)
			second := seedStoppedEntry(t, st, nil, testStart.Add(2*time.Hour), testEnd.Add(2*time.Hour))

			// Both entries fail, so the test doesn't depend on which one the run reaches first
			m := &fakeEntryManager{createErr: map[string]error{first.ID: tc.err, second.ID: tc.err}}

			err := Run(t.Context(), st, m, runClock(0), lockPath)

			checkCode(t, err, tc.wantCode)
			if len(m.creates) != 1 {
				t.Errorf("CreateEntryPage called %d times, want 1", len(m.creates))
			}
			checkStored(t, st, first.ID, "pending", "")
			checkStored(t, st, second.ID, "pending", "")
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
		{
			name:     "rate_limited",
			err:      &clierr.Error{Code: clierr.Offline, Message: "too many requests to Notion", Cause: &notion.TooManyRequestsError{RetryAfterSeconds: new(30)}},
			wantCode: clierr.Offline,
		},
		{name: "access_denied", err: &clierr.Error{Code: clierr.AccessDenied, Message: "invalid Notion API key"}, wantCode: clierr.AccessDenied},
		{name: "bad_request", err: &clierr.Error{Code: clierr.Usage, Message: "rejected request by Notion"}, wantState: "blocked"},
		{name: "not_found", err: &clierr.Error{Code: clierr.NotFound, Message: "object not found in Notion"}, wantState: "blocked"},
		{name: "server_error", err: &clierr.Error{Code: clierr.Internal, Message: "unexpected Notion error"}, wantState: "failed"},
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
