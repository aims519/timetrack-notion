package cli

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestValidateStopFlags_Error(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		at       string
		ago      string
		now      time.Time
		loc      *time.Location
		wantCode clierr.ErrorCode
	}{
		{name: "ago_and_at_given", at: "01:00", ago: "10m", now: now, loc: time.UTC, wantCode: clierr.Usage},
		{name: "invalid_at", at: "invalid", now: now, loc: time.UTC, wantCode: clierr.InvalidTime},
		{name: "invalid_ago", ago: "invalid", now: now, loc: time.UTC, wantCode: clierr.InvalidTime},
		{name: "at_in_future", at: "13:00", now: now, loc: time.UTC, wantCode: clierr.InvalidTime},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sa := stopArgs{
				at:  tc.at,
				ago: tc.ago,
			}
			_, err := validateStopFlags(sa, tc.now, tc.loc)
			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("got %v, want clierr.Error", err)
			}

			if cliErr.Code != tc.wantCode {
				t.Errorf("got %q, want %q", cliErr.Code, tc.wantCode)
			}
		})
	}
}

func TestValidateStopFlags_Success(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		at   string
		ago  string
		now  time.Time
		loc  *time.Location
		want time.Time
	}{
		{name: "no_flags", now: now, loc: time.UTC, want: now},
		{name: "at", at: "01:00", now: now, loc: time.UTC, want: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)},
		{name: "ago", ago: "10m", now: now, loc: time.UTC, want: time.Date(2026, 1, 1, 11, 50, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sa := stopArgs{
				at:  tc.at,
				ago: tc.ago,
			}
			got, err := validateStopFlags(sa, tc.now, tc.loc)

			if err != nil {
				t.Fatalf("got %v, want nil", err)
			}

			if !got.Equal(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// stopOutput is the part of the stop command's JSON output the Run tests check
type stopOutput struct {
	Entry struct {
		ID      string  `json:"id"`
		Start   string  `json:"start"`
		End     *string `json:"end"`
		Seconds *int    `json:"seconds"`
	} `json:"entry"`
	Warnings []any `json:"warnings"`
}

func TestRun_Stop_RunningTimer(t *testing.T) {
	t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
	t.Setenv("TT_TIMEZONE", "UTC")
	const startAt = "2026-01-01T09:00:00Z"
	const stopAt = "2026-01-01T10:00:00Z"

	code, stdout, stderr := runTT(t, "start", "writing tests", "--at", startAt)
	if code != 0 {
		t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
	}
	var started startOutput
	if err := json.Unmarshal(stdout, &started); err != nil {
		t.Fatalf("unable to decode start output %q: %v", stdout, err)
	}

	code, stdout, stderr = runTT(t, "stop", "--at", stopAt)
	if code != 0 {
		t.Fatalf("stop = %d, want 0 (stderr: %q)", code, stderr)
	}

	var got stopOutput
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("unable to decode stop output %q: %v", stdout, err)
	}
	if got.Entry.ID != started.Entry.ID {
		t.Errorf("entry.id want %q, got %q", started.Entry.ID, got.Entry.ID)
	}
	if got.Entry.Start != startAt {
		t.Errorf("entry.start want %q, got %q", startAt, got.Entry.Start)
	}
	if got.Entry.End == nil || *got.Entry.End != stopAt {
		t.Errorf("entry.end want %q, got %v", stopAt, got.Entry.End)
	}
	if got.Entry.Seconds == nil || *got.Entry.Seconds != 3600 {
		t.Errorf("entry.seconds want 3600, got %v", got.Entry.Seconds)
	}
	if got.Warnings == nil {
		t.Errorf("warnings want an array, got null")
	}

	// The timer is stopped, so stopping again is refused
	code, _, stderr = runTT(t, "stop")
	if code != 4 {
		t.Errorf("second stop = %d, want 4 (stderr: %q)", code, stderr)
	}
}

func TestRun_Stop_Errors(t *testing.T) {
	const startAt = "2026-01-01T09:00:00Z"

	tests := []struct {
		name     string
		running  bool // start a timer at startAt before stopping
		args     []string
		wantExit int
		wantCode clierr.ErrorCode
	}{
		{name: "nothing_running", args: []string{"stop"}, wantExit: 4, wantCode: clierr.NoTimerRunning},
		{name: "before_running_start", running: true, args: []string{"stop", "--at", "2026-01-01T08:00:00Z"}, wantExit: 2, wantCode: clierr.InvalidTime},
		{name: "at_running_start", running: true, args: []string{"stop", "--at", startAt}, wantExit: 2, wantCode: clierr.InvalidTime},
		{name: "at_and_ago", running: true, args: []string{"stop", "--at", "10:00", "--ago", "10m"}, wantExit: 2, wantCode: clierr.Usage},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
			t.Setenv("TT_TIMEZONE", "UTC")

			if tc.running {
				if code, _, stderr := runTT(t, "start", "writing tests", "--at", startAt); code != 0 {
					t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
				}
			}

			code, stdout, stderr := runTT(t, tc.args...)
			if code != tc.wantExit {
				t.Errorf("Run(%v) = %d, want %d (stderr: %q)", tc.args, code, tc.wantExit, stderr)
			}

			// A failed command must write nothing to stdout
			if len(stdout) != 0 {
				t.Errorf("Run(%v) wrote to stdout on failure: %q", tc.args, stdout)
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

			if !tc.running {
				return
			}

			// The timer is still running, so a plain stop succeeds
			if code, _, stderr := runTT(t, "stop"); code != 0 {
				t.Errorf("stop after the failed stop = %d, want 0 (stderr: %q)", code, stderr)
			}
		})
	}
}
