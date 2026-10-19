package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestOpen_Errors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T) string
	}{
		{name: "empty", setup: func(t *testing.T) string { return "" }},
		{name: "special_character", setup: func(t *testing.T) string { return filepath.Join(t.TempDir(), "t?st.db") }},
		{name: "path_is_directory", setup: func(t *testing.T) string { return t.TempDir() }},
		{name: "directory_does_not_exist", setup: func(t *testing.T) string { return filepath.Join(t.TempDir(), "/missing/test.db") }},
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
