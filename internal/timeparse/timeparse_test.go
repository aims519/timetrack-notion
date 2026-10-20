package timeparse

import (
	"errors"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func nowUTC(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
}

func londonTZ(t *testing.T) *time.Location {
	t.Helper()

	tz := "Europe/London"
	loc, err := time.LoadLocation(tz)
	if err != nil {
		t.Fatalf("unable to load TZ %q", tz)
	}

	return loc
}

func TestValidTime_Valid(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		layout string
	}{
		// {name: "hhmm", input: "1:30", layout: "04:05"},    // layout HH:MM
		{name: "rfc3339", input: "2026-09-20T14:30:55Z", layout: time.RFC3339}, // layout YYYY-MM-DDTHH:mm:ssZ
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := nowUTC(t)

			_, err := ValidTime(tc.input, tc.layout, time.UTC, now)
			if err != nil {
				t.Errorf("expected nil, got %v", err)
			}
		})
	}
}

func TestValidTime_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		layout string
	}{
		{name: "layout_mismatch_hhmm", input: "1:30", layout: "04:05"},                // layout HH:MM
		{name: "layout_mismatch_rcc3339", input: "2006-", layout: time.RFC3339},       // layout YYYY-MM-DDTHH:mm:ssZ
		{name: "future_rcc3339", input: "2026-11-20T14:30:55Z", layout: time.RFC3339}, // layout YYYY-MM-DDTHH:mm:ssZ
	}
	for _, tc := range tests {
		now := nowUTC(t)

		t.Run(tc.name, func(t *testing.T) {
			_, err := ValidTime(tc.input, tc.layout, time.UTC, now)

			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Errorf("expected clierr.Error, got %v", err)
			}
		})
	}
}

func TestTodayHHMM_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "valid_startofday", input: "00:00", want: "2026-10-01T00:00:00Z"},
		{name: "valid_hours_only", input: "12:00", want: "2026-10-01T12:00:00Z"},
		{name: "valid_minutes_only", input: "00:34", want: "2026-10-01T00:34:00Z"},
		{name: "valid_hours_and_minutes", input: "14:57", want: "2026-10-01T14:57:00Z"},
		{name: "valid_hours_and_minutes_single_digits", input: "1:7", want: "2026-10-01T01:07:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 01, 15, 30, 15, 0, time.UTC)
			ti, err := TodayAtHHMM(tc.input, now, time.UTC)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := ti.Format(time.RFC3339); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTodayHHMM_ValidDSTBackwards(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "valid_startofday", input: "00:00", want: "2026-10-25T00:00:00+01:00"},
		{name: "valid_not_ambiguous", input: "00:59", want: "2026-10-25T00:59:00+01:00"},
		{name: "valid_post_switch", input: "02:30", want: "2026-10-25T02:30:00Z"},
		{name: "valid_edge", input: "02:00", want: "2026-10-25T02:00:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc := londonTZ(t)
			now := time.Date(2026, 10, 25, 15, 30, 15, 0, loc) // DST change at 02:00 (-1 hr)

			ti, err := TodayAtHHMM(tc.input, now, loc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := ti.Format(time.RFC3339); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTodayHHMM_ValidDSTForwards(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "valid_startofday", input: "00:00", want: "2026-03-29T00:00:00Z"},
		{name: "valid_not_ambiguous", input: "00:59", want: "2026-03-29T00:59:00Z"},
		{name: "valid_edge", input: "02:00", want: "2026-03-29T02:00:00+01:00"},
		{name: "valid_post_switch", input: "02:30", want: "2026-03-29T02:30:00+01:00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc := londonTZ(t)
			now := time.Date(2026, 3, 29, 15, 30, 15, 0, loc) // DST change at 01:00 (+1 hr)

			ti, err := TodayAtHHMM(tc.input, now, loc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := ti.Format(time.RFC3339); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTodayHHMM_ErrorDSTBackwards(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "invalid_ambiguous", input: "01:30"},
		{name: "invalid_ambiguous_edge_start", input: "01:00"},
		{name: "invalid_ambiguous_edge_end", input: "01:59"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc := londonTZ(t)
			now := time.Date(2026, 10, 25, 15, 30, 15, 0, loc) // DST change at 02:00 (- 1hr)

			_, err := TodayAtHHMM(tc.input, now, loc)
			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("expected clierr.Error, got %v", err)
			}

			if cliErr.Code != clierr.InvalidTime {
				t.Errorf("got %v, want %v", cliErr.Code, clierr.InvalidTime)
			}
		})
	}
}

func TestTodayHHMM_ErrorDSTForwards(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "invalid_not_exist", input: "01:30"},
		{name: "invalid_edge_start", input: "01:00"},
		{name: "invalid_edge_end", input: "01:59"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc := londonTZ(t)
			now := time.Date(2026, 03, 29, 15, 30, 15, 0, loc) // DST change at 01:00 (+1 hr)

			ti, err := TodayAtHHMM(tc.input, now, loc)
			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Logf("unexpected result: %v", ti)
				t.Fatalf("expected clierr.Error, got %v", err)
			}

			if cliErr.Code != clierr.InvalidTime {
				t.Errorf("got %v, want %v", cliErr.Code, clierr.InvalidTime)
			}
		})
	}
}

func TestTodayAtHHMM_Error(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "malformed_1", input: "01:00:00"},
		{name: "malformed_2", input: "01:"},
		{name: "malformed_3", input: ":40"},
		{name: "invalid_1", input: "24:40"},
		{name: "invalid_2", input: "15:60"},
		{name: "invalid_3", input: "-04:40"},
		{name: "future", input: "17:00"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 01, 15, 30, 15, 0, time.UTC)
			_, err := TodayAtHHMM(tc.input, now, time.UTC)

			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("expected clierr.Error, got %v", err)
			}

			if cliErr.Code != clierr.InvalidTime {
				t.Errorf("got %v, want %v", cliErr.Code, clierr.InvalidTime)
			}
		})
	}
}
