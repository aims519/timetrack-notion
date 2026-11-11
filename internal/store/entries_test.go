package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

const syncTaskID = "5555555555555555555555555e123456"

// syncAttemptedAt is when the sync under test began. Seeded entries are last updated before it
var syncAttemptedAt = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// syncStoredEntry holds the columns sync writes, read straight from the entries table
type syncStoredEntry struct {
	entryPageID       sql.NullString
	createAttemptedAt sql.NullString
	syncState         string
	syncError         sql.NullString
	syncedAt          sql.NullString
}

func readSyncStoredEntry(t *testing.T, st *Store, id string) syncStoredEntry {
	t.Helper()

	var e syncStoredEntry
	err := st.connectionPool.QueryRowContext(t.Context(), `
		SELECT entry_page_id, create_attempted_at, sync_state, sync_error, synced_at
		FROM entries WHERE id = ?`, id).
		Scan(&e.entryPageID, &e.createAttemptedAt, &e.syncState, &e.syncError, &e.syncedAt)
	if err != nil {
		t.Fatalf("read entry %q failed: %v", id, err)
	}

	return e
}

// setEntryPageID stores a page ID for an entry, as an earlier successful sync would have
func setEntryPageID(t *testing.T, st *Store, id, pageID string) {
	t.Helper()

	_, err := st.connectionPool.ExecContext(t.Context(), `UPDATE entries SET entry_page_id = ? WHERE id = ?`, pageID, id)
	if err != nil {
		t.Fatalf("set entry_page_id on %q failed: %v", id, err)
	}
}

// openSyncTestStore opens a store with one cached task, ready for entries attached to it
func openSyncTestStore(t *testing.T) *Store {
	t.Helper()

	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))
	insertListTasks(t, st, []listTask{{pageID: syncTaskID, name: "Write tests"}})

	return st
}

func TestPendingEntries_Selection(t *testing.T) {
	const (
		start = "2026-11-10T09:00:00.000000000Z"
		end   = "2026-11-10T10:00:00.000000000Z"
	)

	tests := []struct {
		name  string
		entry attachEntry
		want  bool
	}{
		{name: "pending_stopped_attached", entry: attachEntry{start: start, end: end, taskID: syncTaskID, syncState: "pending"}, want: true},
		{name: "failed_retried", entry: attachEntry{start: start, end: end, taskID: syncTaskID, syncState: "failed"}, want: true},
		{name: "synced_skipped", entry: attachEntry{start: start, end: end, taskID: syncTaskID, syncState: "synced"}, want: false},
		{name: "blocked_skipped", entry: attachEntry{start: start, end: end, taskID: syncTaskID, syncState: "blocked"}, want: false},
		{name: "running_waits", entry: attachEntry{start: start, taskID: syncTaskID, syncState: "pending"}, want: false},
		{name: "unattached_waits", entry: attachEntry{start: start, end: end, syncState: "pending"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openSyncTestStore(t)
			tc.entry.id = entryA1
			insertAttachEntries(t, st, []attachEntry{tc.entry})

			got, err := st.PendingEntries(t.Context())
			if err != nil {
				t.Fatalf("PendingEntries() error = %v, want nil", err)
			}

			ids := make([]string, 0, len(got))
			for _, e := range got {
				ids = append(ids, e.ID)
			}
			if found := slices.Contains(ids, entryA1); found != tc.want {
				t.Errorf("PendingEntries() ids = %v, contains %q = %v, want %v", ids, entryA1, found, tc.want)
			}
		})
	}
}

func TestPendingEntries_Fields(t *testing.T) {
	st := openSyncTestStore(t)
	insertAttachEntries(t, st, []attachEntry{{
		id:          entryA1,
		start:       "2026-11-10T09:00:00.000000000Z",
		end:         "2026-11-10T10:30:00.000000000Z",
		description: "pairing on sync",
		taskID:      syncTaskID,
	}})
	setEntryPageID(t, st, entryA1, "page-1")

	got, err := st.PendingEntries(t.Context())
	if err != nil {
		t.Fatalf("PendingEntries() error = %v, want nil", err)
	}
	if len(got) != 1 {
		t.Fatalf("PendingEntries() returned %d entries, want 1", len(got))
	}
	e := got[0]

	wantStart := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 11, 10, 10, 30, 0, 0, time.UTC)

	if e.ID != entryA1 {
		t.Errorf("ID = %q, want %q", e.ID, entryA1)
	}
	if e.Description == nil || *e.Description != "pairing on sync" {
		t.Errorf("Description = %v, want %q", e.Description, "pairing on sync")
	}
	if !e.StartUTC.Equal(wantStart) {
		t.Errorf("StartUTC = %v, want %v", e.StartUTC, wantStart)
	}
	if e.EndUTC == nil || !e.EndUTC.Equal(wantEnd) {
		t.Errorf("EndUTC = %v, want %v", e.EndUTC, wantEnd)
	}
	if e.TaskPageID == nil || *e.TaskPageID != syncTaskID {
		t.Errorf("TaskPageID = %v, want %q", e.TaskPageID, syncTaskID)
	}
	if e.TaskName == nil || *e.TaskName != "Write tests" {
		t.Errorf("TaskName = %v, want %q", e.TaskName, "Write tests")
	}
	if e.EntryPageID == nil || *e.EntryPageID != "page-1" {
		t.Errorf("EntryPageID = %v, want %q", e.EntryPageID, "page-1")
	}
	if e.SyncState != "pending" {
		t.Errorf("SyncState = %q, want %q", e.SyncState, "pending")
	}
}

func TestPendingEntries_NoneReturnsEmpty(t *testing.T) {
	st := openSyncTestStore(t)

	got, err := st.PendingEntries(t.Context())
	if err != nil {
		t.Fatalf("PendingEntries() error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("PendingEntries() = %v, want none", got)
	}
}

func TestSyncAttempt_SetsCreateAttemptedAt(t *testing.T) {
	st := openSyncTestStore(t)
	insertAttachEntries(t, st, []attachEntry{
		{id: entryA1, start: "2026-11-10T09:00:00.000000000Z", end: "2026-11-10T10:00:00.000000000Z", taskID: syncTaskID},
		{id: entryB, start: "2026-11-10T11:00:00.000000000Z", end: "2026-11-10T11:30:00.000000000Z", taskID: syncTaskID},
	})

	if err := st.SyncAttempt(t.Context(), entryA1, syncAttemptedAt); err != nil {
		t.Fatalf("SyncAttempt() error = %v, want nil", err)
	}

	got := readSyncStoredEntry(t, st, entryA1)
	if want := ToDBString(syncAttemptedAt); got.createAttemptedAt.String != want {
		t.Errorf("create_attempted_at = %v, want %q", got.createAttemptedAt, want)
	}

	// Only the named entry is touched
	if other := readSyncStoredEntry(t, st, entryB); other.createAttemptedAt.Valid {
		t.Errorf("other entry create_attempted_at = %q, want null", other.createAttemptedAt.String)
	}
}

func TestSyncAttempt_UnknownEntry(t *testing.T) {
	st := openSyncTestStore(t)

	err := st.SyncAttempt(t.Context(), entryA1, syncAttemptedAt)

	var cliErr *clierr.Error
	if !errors.As(err, &cliErr) {
		t.Fatalf("SyncAttempt() error = %v, want *clierr.Error", err)
	}
	if cliErr.Code != clierr.Usage {
		t.Errorf("error code = %q, want %q", cliErr.Code, clierr.Usage)
	}
}

func TestCompleteSync(t *testing.T) {
	syncedAt := syncAttemptedAt.Add(2 * time.Second)

	tests := []struct {
		name          string
		storedPageID  *string // entry_page_id before the sync
		updatedAt     string  // updated_at before the sync
		state         string
		syncError     *string
		pageID        *string
		syncedAt      *time.Time
		wantState     string
		wantPageID    *string
		wantSyncError *string
		wantSyncedAt  *time.Time
	}{
		{
			name:       "synced_records_page",
			updatedAt:  "2026-11-10T10:00:00.000000000Z",
			state:      "synced",
			pageID:     ptr("page-1"),
			syncedAt:   &syncedAt,
			wantState:  "synced",
			wantPageID: ptr("page-1"),
			// sync_error is cleared on success
			wantSyncedAt: &syncedAt,
		},
		{
			name:          "failed_records_error",
			updatedAt:     "2026-11-10T10:00:00.000000000Z",
			state:         "failed",
			syncError:     ptr("server error"),
			wantState:     "failed",
			wantSyncError: ptr("server error"),
		},
		{
			name:          "blocked_records_error",
			updatedAt:     "2026-11-10T10:00:00.000000000Z",
			state:         "blocked",
			syncError:     ptr("rejected request by Notion"),
			wantState:     "blocked",
			wantSyncError: ptr("rejected request by Notion"),
		},
		{
			// A failed update must not lose the page an earlier sync created (§4, §6.3)
			name:          "failed_keeps_stored_page",
			storedPageID:  ptr("page-1"),
			updatedAt:     "2026-11-10T10:00:00.000000000Z",
			state:         "failed",
			syncError:     ptr("server error"),
			wantState:     "failed",
			wantPageID:    ptr("page-1"),
			wantSyncError: ptr("server error"),
		},
		{
			// Edited after the sync read it, so it stays pending for the next run (§6.3)
			name:      "edited_during_sync_stays_pending",
			updatedAt: ToDBString(syncAttemptedAt.Add(time.Second)),
			state:     "synced",
			pageID:    ptr("page-1"),
			syncedAt:  &syncedAt,
			wantState: "pending",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openSyncTestStore(t)
			insertAttachEntries(t, st, []attachEntry{
				{id: entryA1, start: "2026-11-10T09:00:00.000000000Z", end: "2026-11-10T10:00:00.000000000Z", taskID: syncTaskID},
			})
			_, err := st.connectionPool.ExecContext(t.Context(), `UPDATE entries SET updated_at = ?, entry_page_id = ? WHERE id = ?`,
				tc.updatedAt, tc.storedPageID, entryA1)
			if err != nil {
				t.Fatalf("seed entry failed: %v", err)
			}

			err = st.CompleteSync(t.Context(), entryA1, tc.state, tc.syncError, tc.pageID, syncAttemptedAt, tc.syncedAt)
			if err != nil {
				t.Fatalf("CompleteSync() error = %v, want nil", err)
			}

			got := readSyncStoredEntry(t, st, entryA1)
			if got.syncState != tc.wantState {
				t.Errorf("sync_state = %q, want %q", got.syncState, tc.wantState)
			}
			checkNullString(t, "entry_page_id", got.entryPageID, tc.wantPageID)
			checkNullString(t, "sync_error", got.syncError, tc.wantSyncError)

			var wantSyncedAt *string
			if tc.wantSyncedAt != nil {
				wantSyncedAt = ptr(ToDBString(*tc.wantSyncedAt))
			}
			checkNullString(t, "synced_at", got.syncedAt, wantSyncedAt)
		})
	}
}

// checkNullString fails the test unless got holds want, a nil want meaning NULL
func checkNullString(t *testing.T, column string, got sql.NullString, want *string) {
	t.Helper()

	switch {
	case want == nil && got.Valid:
		t.Errorf("%s = %q, want null", column, got.String)
	case want != nil && !got.Valid:
		t.Errorf("%s = null, want %q", column, *want)
	case want != nil && got.String != *want:
		t.Errorf("%s = %q, want %q", column, got.String, *want)
	}
}
