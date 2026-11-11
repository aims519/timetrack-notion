package sync

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/gofrs/flock"
)

func TestRun_LockFree_ReleasesLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "tt.db.lock")

	if err := Run(t.Context(), lockPath); err != nil {
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
		name     string
		lockPath func(t *testing.T) string
		wantCode clierr.ErrorCode
	}{
		{
			name: "lock_held",
			lockPath: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "tt.db.lock")
				holder := flock.New(path)
				locked, err := holder.TryLock()
				if err != nil || !locked {
					t.Fatalf("unable to hold lock for test: locked=%v, err=%v", locked, err)
				}
				t.Cleanup(func() { _ = holder.Unlock() })

				return path
			},
			wantCode: clierr.SyncBusy,
		},
		{
			name: "directory_missing",
			lockPath: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing", "tt.db.lock")
			},
			wantCode: clierr.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.lockPath(t)

			err := Run(t.Context(), path)

			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("Run(%q) = %v, want *clierr.Error", path, err)
			}
			if cliErr.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q", cliErr.Code, tc.wantCode)
			}
		})
	}
}
