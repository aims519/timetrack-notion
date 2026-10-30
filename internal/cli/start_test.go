package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestValidateStartFlags_Error(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		description string
		task        string
		pick        bool
		at          string
		ago         string
		now         time.Time
		loc         *time.Location
		wantCode    clierr.ErrorCode
	}{
		{name: "no_description_or_task", ago: "10m", now: now, loc: time.UTC, wantCode: clierr.Usage},
		{name: "task_and_pick", task: "1.2", pick: true, ago: "10m", now: now, loc: time.UTC, wantCode: clierr.Usage},
		{name: "ago_and_at_given", description: "organising calendar", at: "01:00", ago: "10m", now: now, loc: time.UTC, wantCode: clierr.Usage},
		{name: "invalid_at", description: "organising calendar", at: "invalid", now: now, loc: time.UTC, wantCode: clierr.InvalidTime},
		{name: "invalid_ago", description: "organising calendar", ago: "invalid", now: now, loc: time.UTC, wantCode: clierr.InvalidTime},
		{name: "at_in_future", description: "organising calendar", at: "13:00", now: now, loc: time.UTC, wantCode: clierr.InvalidTime},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sa := startArgs{
				description: tc.description,
				task:        tc.task,
				pickTask:    tc.pick,
				at:          tc.at,
				ago:         tc.ago,
			}
			_, err := validateStartFlags(sa, tc.now, tc.loc)
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

func TestValidateStartFlags_Success(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		description string
		task        string
		pick        bool
		at          string
		ago         string
		now         time.Time
		loc         *time.Location
	}{
		{name: "description_only", description: "organising calendar", now: now, loc: time.UTC},
		{name: "task_only", task: "1.1", now: now, loc: time.UTC},
		{name: "pick_only", pick: true, now: now, loc: time.UTC},
		{name: "description_at", description: "organising calendar", at: "01:00", now: now, loc: time.UTC},
		{name: "description_ago", description: "organising calendar", ago: "10m", now: now, loc: time.UTC},
		{name: "task_at", task: "1.1", at: "01:00", now: now, loc: time.UTC},
		{name: "task_ago", task: "1.1", ago: "10m", now: now, loc: time.UTC},
		{name: "pick_at", pick: true, at: "01:00", now: now, loc: time.UTC},
		{name: "pick_ago", pick: true, ago: "10m", now: now, loc: time.UTC},
		{name: "description_task_at", description: "organising calendar", task: "1.1", at: "01:00", now: now, loc: time.UTC},
		{name: "description_task_ago", description: "organising calendar", task: "1.5", ago: "10m", now: now, loc: time.UTC},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sa := startArgs{
				description: tc.description,
				task:        tc.task,
				pickTask:    tc.pick,
				at:          tc.at,
				ago:         tc.ago,
			}
			_, err := validateStartFlags(sa, tc.now, tc.loc)

			if err != nil {
				t.Fatalf("got %v, want nil", err)
			}
		})
	}
}

func TestRun_Start_UnimplementedFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantFlag string // the flag the error message should name
	}{
		{"task_only", []string{"start", "--task", "1.1"}, "--task"},
		{"task_with_description", []string{"start", "--task", "1.1", "organising calendar"}, "--task"},
		{"pick_only", []string{"start", "--pick"}, "--pick"},
		{"pick_with_description", []string{"start", "--pick", "organising calendar"}, "--pick"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
			t.Setenv("TT_TIMEZONE", "UTC")
			args := append(slices.Clone(tc.args), "--output", "json")
			var stdout, stderr bytes.Buffer

			if got := Run(context.Background(), args, &stdout, &stderr); got != 2 {
				t.Errorf("Run(%v) = %d, want 2 (stderr: %q)", args, got, stderr.String())
			}

			// A failed command must write nothing to stdout
			if stdout.Len() != 0 {
				t.Errorf("Run(%v) wrote to stdout on failure: %q", args, stdout.String())
			}

			var doc struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stderr.Bytes(), &doc); err != nil {
				t.Fatalf("unable to decode error document %q: %v", stderr.String(), err)
			}
			if doc.Error.Code != string(clierr.Usage) {
				t.Errorf("error code want %q, got %q", clierr.Usage, doc.Error.Code)
			}
			if !strings.Contains(doc.Error.Message, tc.wantFlag) {
				t.Errorf("error message want it to name %s, got %q", tc.wantFlag, doc.Error.Message)
			}

			// Nothing was started, so a plain start on the same database succeeds
			stdout.Reset()
			stderr.Reset()
			plain := []string{"start", "organising calendar", "--output", "json"}
			if got := Run(context.Background(), plain, &stdout, &stderr); got != 0 {
				t.Errorf("Run(%v) after rejection = %d, want 0 (stderr: %q)", plain, got, stderr.String())
			}
		})
	}
}

// startOutput is the part of the start command's JSON output the switch tests check
type startOutput struct {
	Entry struct {
		ID    string  `json:"id"`
		Start string  `json:"start"`
		End   *string `json:"end"`
	} `json:"entry"`
	Stopped *struct {
		ID  string  `json:"id"`
		End *string `json:"end"`
	} `json:"stopped"`
}

// runStart runs tt with args and JSON output, returning the exit code, stdout and stderr
func runStart(t *testing.T, args ...string) (int, []byte, []byte) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	got := Run(context.Background(), append(args, "--output", "json"), &stdout, &stderr)
	return got, stdout.Bytes(), stderr.Bytes()
}

func TestRun_Start_Switch(t *testing.T) {
	const firstStart = "2026-01-01T09:00:00Z"
	const switchAt = "2026-01-01T10:00:00Z"

	tests := []struct {
		name        string
		running     bool // start a timer at firstStart before switching
		wantStopped bool
	}{
		{name: "nothing_running", wantStopped: false},
		{name: "timer_running", running: true, wantStopped: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
			t.Setenv("TT_TIMEZONE", "UTC")

			var first startOutput
			if tc.running {
				code, stdout, stderr := runStart(t, "start", "organising calendar", "--at", firstStart)
				if code != 0 {
					t.Fatalf("first start = %d, want 0 (stderr: %q)", code, stderr)
				}
				if err := json.Unmarshal(stdout, &first); err != nil {
					t.Fatalf("unable to decode first start output %q: %v", stdout, err)
				}
			}

			code, stdout, stderr := runStart(t, "start", "--switch", "writing tests", "--at", switchAt)
			if code != 0 {
				t.Fatalf("start --switch = %d, want 0 (stderr: %q)", code, stderr)
			}

			var got startOutput
			if err := json.Unmarshal(stdout, &got); err != nil {
				t.Fatalf("unable to decode start --switch output %q: %v", stdout, err)
			}

			if got.Entry.ID == "" || got.Entry.ID == first.Entry.ID {
				t.Errorf("entry.id want a new entry ID, got %q", got.Entry.ID)
			}
			if got.Entry.Start != switchAt {
				t.Errorf("entry.start want %q, got %q", switchAt, got.Entry.Start)
			}
			if got.Entry.End != nil {
				t.Errorf("entry.end want null, got %q", *got.Entry.End)
			}

			if !tc.wantStopped {
				if got.Stopped != nil {
					t.Errorf("stopped want null, got %+v", *got.Stopped)
				}
				return
			}
			if got.Stopped == nil {
				t.Fatalf("stopped want entry %q, got null", first.Entry.ID)
			}
			if got.Stopped.ID != first.Entry.ID {
				t.Errorf("stopped.id want %q, got %q", first.Entry.ID, got.Stopped.ID)
			}
			// The old entry ends at the instant the new one starts
			if got.Stopped.End == nil || *got.Stopped.End != got.Entry.Start {
				t.Errorf("stopped.end want %q, got %v", got.Entry.Start, got.Stopped.End)
			}
		})
	}
}

func TestRun_Start_SwitchBeforeRunningStart(t *testing.T) {
	t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
	t.Setenv("TT_TIMEZONE", "UTC")

	if code, _, stderr := runStart(t, "start", "organising calendar", "--at", "2026-01-01T09:00:00Z"); code != 0 {
		t.Fatalf("first start = %d, want 0 (stderr: %q)", code, stderr)
	}

	code, stdout, stderr := runStart(t, "start", "--switch", "writing tests", "--at", "2026-01-01T08:00:00Z")
	if code != 2 {
		t.Errorf("start --switch before the running start = %d, want 2 (stderr: %q)", code, stderr)
	}

	// A failed command must write nothing to stdout
	if len(stdout) != 0 {
		t.Errorf("start --switch wrote to stdout on failure: %q", stdout)
	}

	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stderr, &doc); err != nil {
		t.Fatalf("unable to decode error document %q: %v", stderr, err)
	}
	if doc.Error.Code != string(clierr.InvalidTime) {
		t.Errorf("error code want %q, got %q", clierr.InvalidTime, doc.Error.Code)
	}

	// The first timer is still running, so a plain start is refused
	code, _, stderr = runStart(t, "start", "organising calendar")
	if code != 4 {
		t.Errorf("start after the failed switch = %d, want 4 (stderr: %q)", code, stderr)
	}
	if err := json.Unmarshal(stderr, &doc); err != nil {
		t.Fatalf("unable to decode error document %q: %v", stderr, err)
	}
	if doc.Error.Code != string(clierr.TimerRunning) {
		t.Errorf("error code want %q, got %q", clierr.TimerRunning, doc.Error.Code)
	}
}
