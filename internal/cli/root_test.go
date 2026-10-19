package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
		{
			name: "env_override_empty",
			setEnv: func(t *testing.T) string {
				t.Setenv("TT_DB", "")
				return ""
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

			if want == "" {
				return
			}

			ttDir := filepath.Dir(got)
			_, err = os.Stat(ttDir)
			if !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("TT_DB directory %q should not be created", ttDir)
			}
		})
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
