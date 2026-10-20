package timeparse

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func nowUTC(t *testing.T) time.Time {
	t.Helper()
	return time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
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

func resetEnv(t *testing.T) {
	// clean up any local env, restored after test
	t.Setenv("TT_TIMEZONE", "")
	err := os.Unsetenv("TT_TIMEZONE")
	if err != nil {
		t.Fatalf("error unsetting TT_TIMEZONE: %v", err)
	}
}

func TestCurrentLocation_ValidEnv(t *testing.T) {
	tests := []struct {
		name string
		tz   string
		want string
	}{
		{name: "valid_env", tz: "Europe/London", want: "Europe/London"},
		{name: "valid_env_utc", tz: "UTC", want: "UTC"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetEnv(t)

			t.Setenv("TT_TIMEZONE", tc.tz)

			loc, err := CurrentLocation()
			if err != nil {
				t.Fatalf("error reading location %v", err)
			}

			if got := loc.String(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCurrentLocation_InvalidEnv(t *testing.T) {
	tests := []struct {
		name     string
		tz       string
		wantCode clierr.ErrorCode
		setEnv   func(t *testing.T) clierr.ErrorCode
	}{
		{name: "invalid_env", tz: "Junk/TZ", wantCode: clierr.Usage},
		{name: "empty", tz: "", wantCode: clierr.Usage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetEnv(t)

			t.Setenv("TT_TIMEZONE", tc.tz)

			_, err := CurrentLocation()

			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("want clierr.Error, got %v", err)
			}

			if cliErr.Code != tc.wantCode {
				t.Errorf("want %q, got %q", tc.wantCode, cliErr.Code)
			}
		})
	}
}

func TestCurrentLocation_ValidNoEnv(t *testing.T) {
	resetEnv(t)

	got, err := CurrentLocation()
	if err != nil {
		t.Fatalf("unable to retrieve location %v", err)
	}

	if got != time.Local {
		t.Errorf("got %v, want %v", got, time.Local)
	}
}

func TestParseAgo_Valid(t *testing.T) {
	tests := []struct {
		name string
		ago  string
		now  time.Time
		want string
	}{
		{name: "utc_zero", ago: "0m", now: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), want: "2026-01-01T01:00:00Z"},
		{name: "utc_minutes", ago: "5m", now: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), want: "2026-01-01T00:55:00Z"},
		{name: "utc_hours", ago: "2h", now: time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC), want: "2026-01-01T00:00:00Z"},
		{name: "utc_hours_and_minutes", ago: "3h45m", now: time.Date(2026, 1, 1, 4, 0, 0, 0, time.UTC), want: "2026-01-01T00:15:00Z"},
		{name: "utc_over_1_day", ago: "25h30m", now: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), want: "2026-01-01T22:30:00Z"},
		{name: "utc_crossing_day", ago: "1h30m", now: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), want: "2025-12-31T23:30:00Z"},
		{name: "london_valid", ago: "1h30m", now: time.Date(2026, 1, 1, 12, 0, 0, 0, londonTZ(t)), want: "2026-01-01T10:30:00Z"},
		{name: "london_bst_converts_utc", ago: "1h", now: time.Date(2026, 05, 1, 12, 0, 0, 0, londonTZ(t)), want: "2026-05-01T10:00:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ti, err := ParseAgo(tc.ago, tc.now)
			if err != nil {
				t.Fatalf("got error %v", err)
			}

			if got := ti.Format(time.RFC3339); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseAgo_Invalid(t *testing.T) {
	tests := []struct {
		name string
		ago  string
		now  time.Time
	}{
		{name: "utc_invalid_duration", ago: "5t", now: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)},
		{name: "utc_empty_duration", ago: "", now: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)},
		{name: "utc_negative", ago: "-1h", now: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)},
		{name: "utc_day", ago: "1d", now: time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAgo(tc.ago, tc.now)

			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("want clierr.Error, got %v", err)
			}

			if cliErr.Code != clierr.InvalidTime {
				t.Errorf("got %v, want %v", cliErr.Code, clierr.InvalidTime)
			}
		})
	}
}

func TestParseDuration_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "utc_invalid_duration", input: "5t"},
		{name: "utc_empty_duration", input: ""},
		{name: "utc_negative", input: "-1h"},
		{name: "utc_day", input: "1d"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDuration(tc.input)

			var cliErr *clierr.Error
			if !errors.As(err, &cliErr) {
				t.Fatalf("want clierr.Error, got %v", err)
			}

			if cliErr.Code != clierr.InvalidTime {
				t.Errorf("got %v, want %v", cliErr.Code, clierr.InvalidTime)
			}
		})
	}
}

func TestParse_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "hhmm", input: "09:30", want: "2026-10-24T08:30:00Z"},
		{name: "rfc3339_utc", input: "2026-10-24T11:00:00Z", want: "2026-10-24T11:00:00Z"},
		{name: "rfc3339_offset", input: "2026-10-24T12:00:00+01:00", want: "2026-10-24T11:00:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc := londonTZ(t)
			now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) // 13:00 BST

			ti, err := Parse(tc.input, now, loc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if ti.Location() != time.UTC {
				t.Errorf("got location %v, want UTC", ti.Location())
			}

			if got := ti.Format(time.RFC3339); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParse_Error(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "junk", input: "junk"},
		{name: "space_separator", input: "2026-10-24 12:00"},
		{name: "hhmm_future", input: "14:00"},
		{name: "rfc3339_future", input: "2026-10-24T12:30:00Z"},
		{name: "rfc3339_lowercase", input: "2026-10-24t12:30:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc := londonTZ(t)
			now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC) // 13:00 BST

			_, err := Parse(tc.input, now, loc)

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

func TestParseRFC3339_Valid(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "rfc3339", input: "2026-09-20T14:30:55Z", want: "2026-09-20T14:30:55Z"},
		// The day before a DST change, which timeRepeats once flagged as ambiguous
		{name: "rfc3339_offset", input: "2026-10-24T12:00:00+01:00", want: "2026-10-24T11:00:00Z"},
		// The repeated 01:30 on the DST change day, made unambiguous by the offset
		{name: "rfc3339_repeated_bst", input: "2026-10-25T01:30:00+01:00", want: "2026-10-25T00:30:00Z"},
		{name: "rfc3339_repeated_gmt", input: "2026-10-25T01:30:00+00:00", want: "2026-10-25T01:30:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := nowUTC(t)

			ti, err := parseRFC3339(tc.input, now)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := ti.UTC().Format(time.RFC3339); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseRFC3339_Error(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "malformed", input: "2006-"},
		{name: "no_offset", input: "2026-10-24T12:00:00"},
		{name: "future", input: "2026-11-20T14:30:55Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := nowUTC(t)

			_, err := parseRFC3339(tc.input, now)

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

func TestTodayAtHHMM_Valid(t *testing.T) {
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
			ti, err := todayAtHHMM(tc.input, now, time.UTC)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := ti.Format(time.RFC3339); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTodayAtHHMM_ValidDSTBackwards(t *testing.T) {
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

			ti, err := todayAtHHMM(tc.input, now, loc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := ti.Format(time.RFC3339); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTodayAtHHMM_ValidDSTForwards(t *testing.T) {
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

			ti, err := todayAtHHMM(tc.input, now, loc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := ti.Format(time.RFC3339); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTodayAtHHMM_ErrorDSTBackwards(t *testing.T) {
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

			_, err := todayAtHHMM(tc.input, now, loc)
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

func TestTodayAtHHMM_ErrorDSTForwards(t *testing.T) {
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

			ti, err := todayAtHHMM(tc.input, now, loc)
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
			_, err := todayAtHHMM(tc.input, now, time.UTC)

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
