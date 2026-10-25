package cli

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
)

func marshalUTCTime(t *testing.T, utc output.UTCTime) string {
	t.Helper()
	data, err := json.Marshal(utc)
	if err != nil {
		t.Fatalf("unable to marshal UTCTime: %v", err)
	}

	return string(data)
}

func TestMapDBEntry_RunningEntry(t *testing.T) {
	description := "writing tests"

	tests := []struct {
		name        string
		description *string
		startUTC    time.Time
		wantStart   string
	}{
		{"with_description", &description, time.Date(2026, 1, 1, 9, 30, 15, 0, time.UTC), `"2026-01-01T09:30:15Z"`},
		{"without_description", nil, time.Date(2026, 1, 1, 9, 30, 15, 0, time.UTC), `"2026-01-01T09:30:15Z"`},
		{"nanoseconds_in_start", &description, time.Date(2026, 1, 1, 9, 30, 15, 123456789, time.UTC), `"2026-01-01T09:30:15Z"`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := store.Entry{
				ID:          "0605115f-a394-4b6d-a8f7-528408bbc44f",
				Description: tc.description,
				StartUTC:    tc.startUTC,
				SyncState:   "pending",
				UpdatedAt:   tc.startUTC,
			}

			got := mapDBEntry(e)

			if got.ID != e.ID {
				t.Errorf("ID want %q, got %q", e.ID, got.ID)
			}

			if start := marshalUTCTime(t, got.Start); start != tc.wantStart {
				t.Errorf("Start want %s, got %s", tc.wantStart, start)
			}

			if tc.description == nil {
				if got.Description != nil {
					t.Errorf("Description want nil, got %q", *got.Description)
				}
			} else if got.Description == nil || *got.Description != *tc.description {
				t.Errorf("Description want %q, got %v", *tc.description, got.Description)
			}

			if got.SyncState != e.SyncState {
				t.Errorf("SyncState want %q, got %q", e.SyncState, got.SyncState)
			}

			// A running entry has no end, so nothing derived from it is set
			if got.End != nil {
				t.Errorf("End want nil, got %v", got.End)
			}
			if got.Seconds != nil {
				t.Errorf("Seconds want nil, got %d", *got.Seconds)
			}
			if got.Minutes != nil {
				t.Errorf("Minutes want nil, got %d", *got.Minutes)
			}

			if got.Task != nil {
				t.Errorf("Task want nil, got %+v", *got.Task)
			}
			if got.SyncError != nil {
				t.Errorf("SyncError want nil, got %q", *got.SyncError)
			}
			if got.Deleted {
				t.Errorf("Deleted want false, got true")
			}
		})
	}
}

func TestMapDBEntry_StoppedEntry(t *testing.T) {
	// Seconds and minutes are separate measures, not parts of one duration: seconds is the
	// real length, and minutes is the difference of the start and end truncated to the
	// minute, so 09:01:50 to 09:02:10 is 20 seconds and 1 minute (DESIGN.md §5)
	tests := []struct {
		name        string
		startUTC    time.Time
		endUTC      time.Time
		wantEnd     string
		wantSeconds int
		wantMinutes int
	}{
		{"whole_minutes", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 10, 12, 0, 0, time.UTC), `"2026-01-01T10:12:00Z"`, 4320, 72},
		{"short_span_across_minute", time.Date(2026, 1, 1, 9, 1, 50, 0, time.UTC), time.Date(2026, 1, 1, 9, 2, 10, 0, time.UTC), `"2026-01-01T09:02:10Z"`, 20, 1},
		{"zero_minutes_within_minute", time.Date(2026, 1, 1, 9, 1, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 1, 59, 0, time.UTC), `"2026-01-01T09:01:59Z"`, 59, 0},
		{"ninety_seconds", time.Date(2026, 1, 1, 9, 0, 30, 0, time.UTC), time.Date(2026, 1, 1, 9, 2, 0, 0, time.UTC), `"2026-01-01T09:02:00Z"`, 90, 2},
		{"cross_midnight", time.Date(2026, 1, 1, 23, 59, 30, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 30, 0, time.UTC), `"2026-01-02T00:00:30Z"`, 60, 1},
		// Seconds are rounded to the nearest second, half away from zero
		{"seconds_round_down", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 20, 400000000, time.UTC), `"2026-01-01T09:00:20Z"`, 20, 0},
		{"seconds_round_half_up", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 20, 500000000, time.UTC), `"2026-01-01T09:00:20Z"`, 21, 0},
		{"seconds_round_up", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 20, 700000000, time.UTC), `"2026-01-01T09:00:20Z"`, 21, 0},
		{"seconds_round_with_fractional_start", time.Date(2026, 1, 1, 9, 0, 0, 800000000, time.UTC), time.Date(2026, 1, 1, 9, 0, 21, 100000000, time.UTC), `"2026-01-01T09:00:21Z"`, 20, 0},
		{"under_half_second_rounds_to_zero", time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 9, 0, 0, 400000000, time.UTC), `"2026-01-01T09:00:00Z"`, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			end := tc.endUTC
			e := store.Entry{
				ID:        "0605115f-a394-4b6d-a8f7-528408bbc44f",
				StartUTC:  tc.startUTC,
				EndUTC:    &end,
				SyncState: "pending",
				UpdatedAt: tc.endUTC,
			}

			got := mapDBEntry(e)

			if got.End == nil {
				t.Errorf("End want %s, got nil", tc.wantEnd)
			} else if gotEnd := marshalUTCTime(t, *got.End); gotEnd != tc.wantEnd {
				t.Errorf("End want %s, got %s", tc.wantEnd, gotEnd)
			}

			if got.Seconds == nil {
				t.Errorf("Seconds want %d, got nil", tc.wantSeconds)
			} else if *got.Seconds != tc.wantSeconds {
				t.Errorf("Seconds want %d, got %d", tc.wantSeconds, *got.Seconds)
			}

			// Minutes come from the start and end truncated to the minute (DESIGN.md §5),
			// so 0 is a real value for a stopped entry, not nil
			if got.Minutes == nil {
				t.Errorf("Minutes want %d, got nil", tc.wantMinutes)
			} else if *got.Minutes != tc.wantMinutes {
				t.Errorf("Minutes want %d, got %d", tc.wantMinutes, *got.Minutes)
			}
		})
	}
}
