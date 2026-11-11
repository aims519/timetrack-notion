package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/gofrs/flock"
)

func TestSyncLockFilePath(t *testing.T) {
	tests := []struct {
		name   string
		dbPath string
		want   string
	}{
		{name: "default_name", dbPath: "/home/u/.local/share/tt/tt.db", want: "/home/u/.local/share/tt/tt.db.lock"},
		{name: "custom_name", dbPath: "/tmp/dev/work.db", want: "/tmp/dev/work.db.lock"},
		{name: "no_extension", dbPath: "/tmp/dev/tt", want: "/tmp/dev/tt.lock"},
		{name: "relative", dbPath: "dev/tt.db", want: "dev/tt.db.lock"},
		{name: "bare_file", dbPath: "tt.db", want: "tt.db.lock"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.FromSlash(tc.dbPath)
			want := filepath.FromSlash(tc.want)

			got := syncLockFilePath(dbPath)
			if got != want {
				t.Errorf("syncLockFilePath(%q) = %q, want %q", dbPath, got, want)
			}
		})
	}
}

func TestRun_Sync_Lock(t *testing.T) {
	tests := []struct {
		name     string
		held     bool // another process holds the sync lock
		wantExit int
		wantCode clierr.ErrorCode
	}{
		{name: "lock_free", wantExit: 0},
		{name: "lock_held", held: true, wantExit: 4, wantCode: clierr.SyncBusy},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "test.db")
			t.Setenv("TT_DB", dbPath)

			if tc.held {
				// Built by hand rather than with syncLockFilePath, so the test
				// also checks the lock sits beside the TT_DB file (§6.5)
				holder := flock.New(dbPath + ".lock")
				locked, err := holder.TryLock()
				if err != nil || !locked {
					t.Fatalf("unable to hold lock for test: locked=%v, err=%v", locked, err)
				}
				t.Cleanup(func() { _ = holder.Unlock() })
			}

			code, stdout, stderr := runTT(t, "sync")
			if code != tc.wantExit {
				t.Fatalf("Run(sync) = %d, want %d (stderr: %q)", code, tc.wantExit, stderr)
			}

			if tc.wantExit == 0 {
				return
			}

			// A failed command must write nothing to stdout
			if len(stdout) != 0 {
				t.Errorf("Run(sync) wrote to stdout on failure: %q", stdout)
			}

			var doc struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr, &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr, err)
			}
			if doc.Error.Code != string(tc.wantCode) {
				t.Errorf("error code want %q, got %q", tc.wantCode, doc.Error.Code)
			}
		})
	}
}
