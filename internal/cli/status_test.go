package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// statusOutput is the part of the status command's JSON output the Run tests check
type statusOutput struct {
	Running *struct {
		ID string `json:"id"`
	} `json:"running"`
	ElapsedSeconds *int `json:"elapsed_seconds"`
	Today          struct {
		Entries int `json:"entries"`
		Minutes int `json:"minutes"`
	} `json:"today"`
	Counts struct {
		Pending int `json:"pending"`
		Blocked int `json:"blocked"`
		Waiting int `json:"waiting"`
	} `json:"counts"`
	Connected bool `json:"connected"`
	Warnings  []struct {
		Code string `json:"code"`
	} `json:"warnings"`
}

func runStatus(t *testing.T) statusOutput {
	t.Helper()

	// status always exits 0 (DESIGN.md §3.1)
	code, stdout, stderr := runTT(t, "status")
	if code != 0 {
		t.Fatalf("status = %d, want 0 (stderr: %q)", code, stderr)
	}

	var got statusOutput
	if err := json.Unmarshal(stdout, &got); err != nil {
		t.Fatalf("unable to decode status output %q: %v", stdout, err)
	}
	return got
}

func TestRun_Status_EmptyDatabase(t *testing.T) {
	t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
	t.Setenv("TT_TIMEZONE", "UTC")

	got := runStatus(t)

	if got.Running != nil {
		t.Errorf("running want null, got %+v", *got.Running)
	}
	if got.ElapsedSeconds != nil {
		t.Errorf("elapsed_seconds want null, got %d", *got.ElapsedSeconds)
	}
	if got.Today.Entries != 0 || got.Today.Minutes != 0 {
		t.Errorf("today want 0 entries and 0 minutes, got %+v", got.Today)
	}
	if got.Counts.Pending != 0 || got.Counts.Blocked != 0 || got.Counts.Waiting != 0 {
		t.Errorf("counts want all 0, got %+v", got.Counts)
	}
	if got.Connected {
		t.Errorf("connected want false before init, got true")
	}
	if got.Warnings == nil || len(got.Warnings) != 0 {
		t.Errorf("warnings want an empty array, got %v", got.Warnings)
	}
}

func TestRun_Status_RunningTimer(t *testing.T) {
	t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
	t.Setenv("TT_TIMEZONE", "UTC")

	before := time.Now().UTC()
	code, stdout, stderr := runTT(t, "start", "writing tests", "--ago", "30m")
	if code != 0 {
		t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
	}
	var started startOutput
	if err := json.Unmarshal(stdout, &started); err != nil {
		t.Fatalf("unable to decode start output %q: %v", stdout, err)
	}

	got := runStatus(t)

	if got.Running == nil {
		t.Fatalf("running want entry %q, got null", started.Entry.ID)
	}
	if got.Running.ID != started.Entry.ID {
		t.Errorf("running.id want %q, got %q", started.Entry.ID, got.Running.ID)
	}
	// The test's own run time is added to the 30 minutes, so allow a little over
	if got.ElapsedSeconds == nil || *got.ElapsedSeconds < 1800 || *got.ElapsedSeconds > 1860 {
		t.Errorf("elapsed_seconds want about 1800, got %v", got.ElapsedSeconds)
	}
	// A running entry is waiting until it stops (DESIGN.md §2)
	if got.Counts.Waiting != 1 {
		t.Errorf("counts.waiting want 1, got %d", got.Counts.Waiting)
	}
	if len(got.Warnings) != 0 {
		t.Errorf("warnings want none, got %v", got.Warnings)
	}

	// Started 30 minutes ago, the entry belongs to yesterday when the test runs just after
	// midnight, so work out which day it started on rather than assume today
	startedToday := before.Add(-30 * time.Minute).Truncate(24 * time.Hour).Equal(before.Truncate(24 * time.Hour))
	wantEntries := 0
	if startedToday {
		wantEntries = 1
	}
	if got.Today.Entries != wantEntries {
		t.Errorf("today.entries want %d (started today: %v), got %d", wantEntries, startedToday, got.Today.Entries)
	}
}

func TestRun_Status_StaleTimer(t *testing.T) {
	t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
	t.Setenv("TT_TIMEZONE", "UTC")

	at := time.Now().UTC().Add(-13 * time.Hour).Format(time.RFC3339)
	if code, _, stderr := runTT(t, "start", "left on", "--at", at); code != 0 {
		t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
	}

	t.Run("json", func(t *testing.T) {
		got := runStatus(t)

		if len(got.Warnings) != 1 || got.Warnings[0].Code != "stale_timer" {
			t.Errorf("warnings want one stale_timer, got %v", got.Warnings)
		}
	})

	t.Run("table", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		args := []string{"status", "--output", "table"}
		if code := Run(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("Run(%v) = %d, want 0 (stderr: %q)", args, code, stderr.String())
		}

		// In table output warnings go to stderr, apart from the data on stdout (DESIGN.md §8)
		if !strings.Contains(stderr.String(), "12 hours") {
			t.Errorf("stderr want the stale timer warning, got %q", stderr.String())
		}
		if strings.Contains(stdout.String(), "12 hours") {
			t.Errorf("stdout want no warning, got %q", stdout.String())
		}
		if !strings.Contains(stdout.String(), "Tracking") {
			t.Errorf("stdout want the running entry, got %q", stdout.String())
		}
	})
}

func TestRun_Status_StoppedTimer(t *testing.T) {
	t.Setenv("TT_DB", filepath.Join(t.TempDir(), "test.db"))
	t.Setenv("TT_TIMEZONE", "UTC")

	if code, _, stderr := runTT(t, "start", "writing tests", "--at", "2026-01-01T09:00:00Z"); code != 0 {
		t.Fatalf("start = %d, want 0 (stderr: %q)", code, stderr)
	}
	if code, _, stderr := runTT(t, "stop", "--at", "2026-01-01T10:00:00Z"); code != 0 {
		t.Fatalf("stop = %d, want 0 (stderr: %q)", code, stderr)
	}

	got := runStatus(t)

	if got.Running != nil {
		t.Errorf("running want null, got %+v", *got.Running)
	}
	if got.ElapsedSeconds != nil {
		t.Errorf("elapsed_seconds want null, got %d", *got.ElapsedSeconds)
	}
	// A stopped entry with no task is still waiting
	if got.Counts.Waiting != 1 || got.Counts.Pending != 0 {
		t.Errorf("counts want 1 waiting and 0 pending, got %+v", got.Counts)
	}
	// It started on an earlier day, so it isn't part of today
	if got.Today.Entries != 0 {
		t.Errorf("today.entries want 0, got %d", got.Today.Entries)
	}
}
