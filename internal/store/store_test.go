package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// testContextID is the single context the first migration creates.
const testContextID = "00000000-0000-0000-0000-000000000000"

func TestOpen_Errors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) string
	}{
		{name: "empty", setup: func(t *testing.T) string { return "" }},
		{name: "special_character", setup: func(t *testing.T) string { return filepath.Join(t.TempDir(), "t?st.db") }},
		{name: "path_is_directory", setup: func(t *testing.T) string { return t.TempDir() }},
		{name: "directory_does_not_exist", setup: func(t *testing.T) string { return filepath.Join(t.TempDir(), "missing/test.db") }},
		{name: "not_a_db_file",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				dbPath := filepath.Join(dir, "test.db")
				err := os.WriteFile(dbPath, []byte("junk data"), 0o600) // owner file permission
				if err != nil {
					t.Fatalf("failure to write junk db file %v", err)
				}

				return dbPath
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := tc.setup(t)
			st, err := Open(t.Context(), dbPath)
			if err == nil {
				t.Errorf("want error on store.Open(), got nil")
				_ = st.Close()
				return
			}

			var errcli *clierr.Error
			if !errors.As(err, &errcli) {
				t.Errorf("want clierrError, got %T", err)
				return
			}

			if errcli.Code != clierr.DatabaseUnusable {
				t.Errorf("want errcli.Code = %q, got %q", clierr.DatabaseUnusable, errcli.Code)
			}

			if got := errcli.Details["path"]; got != dbPath {
				t.Errorf("want errcli.Details[\"path\"] = %q, got %q", dbPath, got)
			}
		})
	}
}

func TestOpen(t *testing.T) {
	dir := t.TempDir()
	tempDbPath := filepath.Join(dir, "test.db")

	st, err := Open(t.Context(), tempDbPath)
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

	// Check foreign keys enabled
	var foreignKeys int
	err = st.connectionPool.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("got foreign_keys = %v, want 1", foreignKeys)
	}

	// Check busy timeout
	var busyTimeout int
	err = st.connectionPool.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&busyTimeout)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("got busyTimeout = %v, want 5000", busyTimeout)
	}

	// Check write-ahead journaling
	var journalMode string
	err = st.connectionPool.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("got journalMode = %q, want %q", journalMode, "wal")
	}
}

func TestEntries_MinimalInsertDefaults(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

	_, err := st.connectionPool.ExecContext(t.Context(),
		"INSERT INTO entries (id, context_id, start_utc, updated_at) VALUES (?, ?, ?, ?)",
		"entry-1", testContextID, "2026-10-19T09:00:00.000000000Z", "2026-10-19T09:00:00.000000000Z")
	if err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// Nullable columns mean "hasn't happened yet", so they must start NULL
	var endUTC, createAttemptedAt, syncedAt, deletedAt sql.NullString
	var syncState string
	err = st.connectionPool.QueryRowContext(t.Context(),
		"SELECT end_utc, create_attempted_at, synced_at, deleted_at, sync_state FROM entries WHERE id = ?", "entry-1").
		Scan(&endUTC, &createAttemptedAt, &syncedAt, &deletedAt, &syncState)
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	nullable := []struct {
		name  string
		value sql.NullString
	}{
		{name: "end_utc", value: endUTC},
		{name: "create_attempted_at", value: createAttemptedAt},
		{name: "synced_at", value: syncedAt},
		{name: "deleted_at", value: deletedAt},
	}
	for _, col := range nullable {
		if col.value.Valid {
			t.Errorf("got %s = %q, want NULL", col.name, col.value.String)
		}
	}
	if syncState != "pending" {
		t.Errorf("got sync_state = %q, want %q", syncState, "pending")
	}
}

func TestEntries_Constraints(t *testing.T) {
	const insert = "INSERT INTO entries (id, context_id, start_utc, end_utc, description, sync_state, updated_at, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)"
	const start = "2026-10-19T09:00:00.000000000Z"
	const end = "2026-10-19T10:00:00.000000000Z"

	// entry holds the columns each case varies; args turns one into insert arguments
	type entry struct {
		id, contextID, start, syncState string
		end, description, deletedAt     any
	}
	args := func(e entry) []any {
		return []any{e.id, e.contextID, e.start, e.end, e.description, e.syncState, start, e.deletedAt}
	}
	running := func(id string) entry {
		return entry{id: id, contextID: testContextID, start: start, syncState: "pending"}
	}

	tests := []struct {
		name     string
		existing []entry
		insert   entry
		wantCode int // 0 means the insert must succeed
	}{
		{name: "valid_running", insert: running("b")},
		{name: "unknown_context",
			insert:   entry{id: "b", contextID: "missing", start: start, syncState: "pending"},
			wantCode: sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY},
		{name: "second_running",
			existing: []entry{running("a")},
			insert:   running("b"),
			wantCode: sqlite3.SQLITE_CONSTRAINT_UNIQUE},
		{name: "second_entry_stopped",
			existing: []entry{running("a")},
			insert:   entry{id: "b", contextID: testContextID, start: start, end: end, syncState: "pending"}},
		{name: "running_after_deleted_running",
			existing: []entry{{id: "a", contextID: testContextID, start: start, syncState: "pending", deletedAt: end}},
			insert:   running("b")},
		{name: "end_before_start",
			insert:   entry{id: "b", contextID: testContextID, start: end, end: start, syncState: "pending"},
			wantCode: sqlite3.SQLITE_CONSTRAINT_CHECK},
		{name: "end_equals_start",
			insert:   entry{id: "b", contextID: testContextID, start: start, end: start, syncState: "pending"},
			wantCode: sqlite3.SQLITE_CONSTRAINT_CHECK},
		{name: "unknown_sync_state",
			insert:   entry{id: "b", contextID: testContextID, start: start, syncState: "done"},
			wantCode: sqlite3.SQLITE_CONSTRAINT_CHECK},
		{name: "description_at_limit",
			insert: entry{id: "b", contextID: testContextID, start: start, syncState: "pending", description: strings.Repeat("a", 2000)}},
		{name: "description_too_long",
			insert:   entry{id: "b", contextID: testContextID, start: start, syncState: "pending", description: strings.Repeat("a", 2001)},
			wantCode: sqlite3.SQLITE_CONSTRAINT_CHECK},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := openTestStore(t, filepath.Join(t.TempDir(), "test.db"))

			for _, e := range tc.existing {
				_, err := st.connectionPool.ExecContext(t.Context(), insert, args(e)...)
				if err != nil {
					t.Fatalf("existing entry %q: insert failed: %v", e.id, err)
				}
			}

			_, err := st.connectionPool.ExecContext(t.Context(), insert, args(tc.insert)...)
			if tc.wantCode == 0 {
				if err != nil {
					t.Errorf("want insert to succeed, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want constraint error %v, got nil", tc.wantCode)
			}

			var errSQLite *sqlite.Error
			if !errors.As(err, &errSQLite) {
				t.Fatalf("want *sqlite.Error, got %T: %v", err, err)
			}
			if errSQLite.Code() != tc.wantCode {
				t.Errorf("want code %v, got %v: %v", tc.wantCode, errSQLite.Code(), err)
			}
		})
	}
}

// openTestStore opens the store at dbPath and closes it when the test ends.
func openTestStore(t *testing.T, dbPath string) *Store {
	t.Helper()

	st, err := Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("unable to open database: %v", err)
	}
	t.Cleanup(func() {
		errClose := st.Close()
		if errClose != nil {
			t.Errorf("error closing store %v", errClose)
		}
	})

	return st
}

// countRows counts the rows in table, which must be a trusted constant.
func countRows(t *testing.T, connPool *sql.DB, table string) int {
	t.Helper()

	var n int
	err := connPool.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n)
	if err != nil {
		t.Fatalf("count of %s failed: %v", table, err)
	}

	return n
}
