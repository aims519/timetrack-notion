package sync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/notion"
)

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

			got, err := syncPendingEntry(t.Context(), testEntriesDataSourceID, e, m, c)
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
