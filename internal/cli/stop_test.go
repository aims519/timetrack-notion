package cli

import (
	"errors"
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
