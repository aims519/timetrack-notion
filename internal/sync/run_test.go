package sync

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
	"github.com/gofrs/flock"
)

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

// A 429 or 5xx on the run's first data source check is retried like an entry's, as Notion did
// answer. A network failure there means the first connection failed, so the run aborts at once
// (§6.3). A check that never succeeds stops the run before any entry request, leaving it pending
func TestRun_DataSourceCheck_Retries(t *testing.T) {
	tests := []struct {
		name       string
		firstErr   error // the first check's error
		laterErr   error // every later check's error
		wantChecks int
		wantWaits  []time.Duration  // exact, when Retry-After sets them
		wantBounds []time.Duration  // or each wait is below its bound, for backoff
		wantCode   clierr.ErrorCode // empty when the run carries on
		wantState  string
	}{
		{name: "rate_limited_once", firstErr: rateLimited(2), wantChecks: 2, wantWaits: []time.Duration{2 * time.Second}, wantState: "synced"},
		{
			name:       "server_error_every_attempt",
			firstErr:   serverError,
			laterErr:   serverError,
			wantChecks: testRetrySettings().maxAttempts,
			wantBounds: []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second},
			wantCode:   clierr.Internal,
			wantState:  "pending",
		},
		{
			name:       "offline",
			firstErr:   &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion"},
			wantChecks: 1,
			wantCode:   clierr.Offline,
			wantState:  "pending",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			entry := seedStoppedEntry(t, st, nil, testStart, testEnd)
			m := &fakeEntryManager{dataSourceErr: tc.firstErr, recheckErr: tc.laterErr, createdPageID: "page-1"}
			c := runClock(0)

			err := Run(t.Context(), st, m, c, lockPath, testRetrySettings())

			if len(m.dataSources) != tc.wantChecks {
				t.Errorf("GetDataSource called %d times, want %d", len(m.dataSources), tc.wantChecks)
			}
			if tc.wantBounds != nil {
				if len(c.sleeps) != len(tc.wantBounds) {
					t.Fatalf("waits = %v, want %d of them", c.sleeps, len(tc.wantBounds))
				}
				for i, wait := range c.sleeps {
					if wait < 0 || wait >= tc.wantBounds[i] {
						t.Errorf("wait %d = %v, want at least 0 and below %v", i+1, wait, tc.wantBounds[i])
					}
				}
			} else if !slices.Equal(c.sleeps, tc.wantWaits) {
				t.Errorf("waits = %v, want %v", c.sleeps, tc.wantWaits)
			}

			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("Run() = %v, want nil", err)
				}
				checkStored(t, st, entry.ID, tc.wantState, "page-1")
				return
			}

			checkCode(t, err, tc.wantCode)
			if n := len(m.finds) + len(m.creates) + len(m.updates); n != 0 {
				t.Errorf("Run() made %d entry requests after a failed check, want 0", n)
			}
			checkStored(t, st, entry.ID, tc.wantState, "")
		})
	}
}

// requestCancelled is the error the client returns when the context is cancelled during a
// request: the HTTP client's *url.Error, wrapping context.Canceled, coded offline
var requestCancelled = &clierr.Error{
	Code:    clierr.Offline,
	Message: "unable to communicate with Notion",
	Cause:   &url.Error{Op: "Get", URL: "https://api.notion.com/v1/data_sources/entries-ds", Err: context.Canceled},
}

// Ctrl-C during the run's first data source check stops the run with cancelled, whether it comes
// during a retry wait or a request, and on any attempt. No entry request is made, and the entry
// stays pending (§6.3)
func TestRun_DataSourceCheck_Cancelled(t *testing.T) {
	tests := []struct {
		name       string
		checkErrs  []error // one per check, in order
		sleepErr   error
		wantChecks int
		wantWaits  int
	}{
		{name: "during_rate_limit_wait", checkErrs: []error{rateLimited(2)}, sleepErr: context.Canceled, wantChecks: 1, wantWaits: 1},
		{name: "during_server_error_wait", checkErrs: []error{serverError}, sleepErr: context.Canceled, wantChecks: 1, wantWaits: 1},
		{name: "during_request", checkErrs: []error{requestCancelled}, wantChecks: 1, wantWaits: 0},
		// On the last attempt the error is returned rather than retried, so it must be mapped first
		{
			name:       "during_last_request",
			checkErrs:  []error{serverError, serverError, serverError, serverError, requestCancelled},
			wantChecks: testRetrySettings().maxAttempts,
			wantWaits:  testRetrySettings().maxAttempts - 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, lockPath := openSyncStore(t, true)
			entry := seedStoppedEntry(t, st, nil, testStart, testEnd)
			m := &fakeEntryManager{dataSourceErrSeq: tc.checkErrs, createdPageID: "page-1"}
			c := runClock(0)
			c.sleepErr = tc.sleepErr

			err := Run(t.Context(), st, m, c, lockPath, testRetrySettings())

			checkCode(t, err, clierr.Cancelled)
			if !errors.Is(err, context.Canceled) {
				t.Errorf("Run() = %v, want it to wrap context.Canceled", err)
			}
			if len(m.dataSources) != tc.wantChecks {
				t.Errorf("GetDataSource called %d times, want %d", len(m.dataSources), tc.wantChecks)
			}
			if len(c.sleeps) != tc.wantWaits {
				t.Errorf("waits = %v, want %d of them", c.sleeps, tc.wantWaits)
			}
			if n := len(m.finds) + len(m.creates) + len(m.updates); n != 0 {
				t.Errorf("Run() made %d entry requests after a cancelled check, want 0", n)
			}
			checkStored(t, st, entry.ID, "pending", "")
		})
	}
}

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
