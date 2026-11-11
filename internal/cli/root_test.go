package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestRun_FlagErrors_ExitWithUsageCode(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"valid_output", []string{"--output", "json"}, 0},
		{"invalid_output", []string{"--output", "xml"}, 2},
		{"unknown_flag", []string{"--bogus"}, 2},
		{"json_unknown_flag", []string{"--output", "json", "--bogus"}, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			got := Run(context.Background(), tc.args, &stdout, &stderr)

			if got != tc.wantCode {
				t.Errorf("Run(%v) = %d, want %d (stderr: %q)", tc.args, got, tc.wantCode, stderr.String())
			}

			if tc.wantCode != 0 && stderr.Len() == 0 {
				t.Errorf("Run(%v) wrote nothing to stderr on failure", tc.args)
			}

			// A failed command must write nothing to stdout
			if tc.wantCode != 0 && stdout.Len() != 0 {
				t.Errorf("Run(%v) wrote to stdout on failure: %q", tc.args, stdout.String())
			}
		})
	}
}

func envReset(t *testing.T) {
	t.Helper()

	// clean up any local env, restored after test
	t.Setenv("TT_DB", "")
	err := os.Unsetenv("TT_DB")
	if err != nil {
		t.Fatalf("error unsetting TT_DB: %v", err)
	}

	t.Setenv("XDG_DATA_HOME", "")
	err = os.Unsetenv("XDG_DATA_HOME")
	if err != nil {
		t.Fatalf("error unsetting XDG_DATA_HOME: %v", err)
	}

	t.Setenv("HOME", t.TempDir())
}

func TestDBFilePathPerOS_Override(t *testing.T) {
	tests := []struct {
		name   string
		setEnv func(t *testing.T) string
	}{
		{
			name: "env_override_valid",
			setEnv: func(t *testing.T) string {
				tempDir := t.TempDir()
				filePath := filepath.Join(tempDir, "missing", "tt.db")
				t.Setenv("TT_DB", filePath)

				return filePath
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			envReset(t)

			want := tc.setEnv(t)
			got, err := dbFilePathPerOS("darwin")
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}

			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}

			ttDir := filepath.Dir(got)
			_, err = os.Stat(ttDir)
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("TT_DB directory %q should not be created", ttDir)
			}
		})
	}
}

func TestDBFilePathPerOS_EmptyOverride(t *testing.T) {
	tests := []struct {
		name   string
		setEnv func(t *testing.T)
		goos   string
	}{
		{name: "xdg_unset", setEnv: func(t *testing.T) {}, goos: "darwin"},
		{
			name: "xdg_set",
			setEnv: func(t *testing.T) {
				t.Setenv("XDG_DATA_HOME", t.TempDir())
			},
			goos: "darwin",
		},
		{
			name: "windows",
			setEnv: func(t *testing.T) {
				t.Setenv("LOCALAPPDATA", t.TempDir())
			},
			goos: "windows",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			envReset(t)
			tc.setEnv(t)
			t.Setenv("TT_DB", "")

			got, err := dbFilePathPerOS(tc.goos)

			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("dbFilePathPerOS(%q) = %q, %v, want *clierr.Error", tc.goos, got, err)
			}
			if cliErr.Code != clierr.DatabaseUnusable {
				t.Errorf("error code = %q, want %q", cliErr.Code, clierr.DatabaseUnusable)
			}
			if got != "" {
				t.Errorf("path = %q, want empty on error", got)
			}

			// An empty TT_DB is not a request for the default, so the default
			// data directory must not be created
			defaults := []string{
				filepath.Join(os.Getenv("HOME"), ".local", "share", "tt"),
				filepath.Join(os.Getenv("XDG_DATA_HOME"), "tt"),
				filepath.Join(os.Getenv("LOCALAPPDATA"), "tt"),
			}
			for _, dir := range defaults {
				if !filepath.IsAbs(dir) {
					continue // that variable isn't set in this case
				}
				if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("default data directory %q was created", dir)
				}
			}
		})
	}
}

func TestRun_EmptyTTDB_DatabaseUnusable(t *testing.T) {
	envReset(t)
	t.Setenv("TT_DB", "")

	code, stdout, stderr := runTT(t, "status")
	if code != 5 {
		t.Errorf("Run(status) = %d, want 5 (stderr: %q)", code, stderr)
	}

	// A failed command must write nothing to stdout
	if len(stdout) != 0 {
		t.Errorf("Run(status) wrote to stdout on failure: %q", stdout)
	}

	var doc struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr, &doc); err != nil {
		t.Fatalf("unable to decode error document %q: %v", stderr, err)
	}
	if doc.Error.Code != string(clierr.DatabaseUnusable) {
		t.Errorf("error code want %q, got %q", clierr.DatabaseUnusable, doc.Error.Code)
	}

	// database_unusable always carries the path it tried (§8), here the empty TT_DB
	path, ok := doc.Error.Details["path"]
	if !ok {
		t.Fatalf("error details want a path, got %v", doc.Error.Details)
	}
	if path != "" {
		t.Errorf("error details path want %q, got %q", "", path)
	}
}

func TestDBFilePathPerOS_Default(t *testing.T) {
	tests := []struct {
		name   string
		setEnv func(t *testing.T) string
		goos   string
	}{
		{
			name: "xdg_valid",
			setEnv: func(t *testing.T) string {
				dir := t.TempDir()
				t.Setenv("XDG_DATA_HOME", dir)

				return filepath.Join(dir, "tt", "tt.db")
			},
			goos: "darwin",
		},
		{
			name: "xdg_valid_windows",
			setEnv: func(t *testing.T) string {
				dir := t.TempDir()
				t.Setenv("XDG_DATA_HOME", dir)

				return filepath.Join(dir, "tt", "tt.db")
			},
			goos: "windows",
		},
		{
			name: "xdg_empty_home_fallback",
			setEnv: func(t *testing.T) string {
				home := t.TempDir()
				t.Setenv("XDG_DATA_HOME", "")
				t.Setenv("HOME", home)

				return filepath.Join(home, ".local", "share", "tt", "tt.db")
			},
			goos: "darwin",
		},
		{
			name: "xdg_relative_home_fallback",
			setEnv: func(t *testing.T) string {
				home := t.TempDir()
				t.Setenv("XDG_DATA_HOME", "relative")
				t.Setenv("HOME", home)

				return filepath.Join(home, ".local", "share", "tt", "tt.db")
			},
			goos: "darwin",
		},
		{
			name: "ttdb_and_xdg_unset_home_fallback",
			setEnv: func(t *testing.T) string {
				home := t.TempDir()
				t.Setenv("HOME", home)

				return filepath.Join(home, ".local", "share", "tt", "tt.db")
			},
			goos: "darwin",
		},
		{
			name: "ttdb_and_xdg_unset_home_fallback_windows",
			setEnv: func(t *testing.T) string {
				appData := t.TempDir()
				t.Setenv("LOCALAPPDATA", appData)

				return filepath.Join(appData, "tt", "tt.db")
			},
			goos: "windows",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			envReset(t)

			want := tc.setEnv(t)
			got, err := dbFilePathPerOS(tc.goos)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}

			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}

			ttDir := filepath.Dir(got)
			fi, err := os.Stat(ttDir)
			if err != nil {
				t.Fatalf("%q directory doesn't exist or can't be read: %v", ttDir, err)
			}

			if !fi.IsDir() {
				t.Errorf("%q is not a directory", ttDir)
			}

			if fi.Mode().Perm() != 0o700 && runtime.GOOS != "windows" {
				t.Errorf("got permissions %v, want 0o700", fi.Mode().Perm())
			}
		})
	}
}
