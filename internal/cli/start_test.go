package cli

import (
	"errors"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestValidateArgs_Error(t *testing.T) {
	tests := []struct {
		name        string
		ago         string
		at          string
		description string
		task        string
		now         time.Time
		wantCode    clierr.ErrorCode
		wantMessage string
	}{
		{name: "ago_and_at_given", ago: "10m", at: "01:00", description: "organising calendar", wantCode: clierr.Usage},
		{name: "no_description_or_task", ago: "10m", wantCode: clierr.Usage},
		{name: "invalid_at", at: "invalid", description: "organising calendar", wantCode: clierr.InvalidTime},
		{name: "invalid_ago", ago: "invalid", description: "organising calendar", wantCode: clierr.InvalidTime},
		{name: "at_in_future", now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), at: "13:00", description: "organising calendar", wantCode: clierr.InvalidTime},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sa := startArgs{description: tc.description,
				task: tc.task,
				at:   tc.at,
				ago:  tc.ago}
			err := validateFlags(sa, tc.now)
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

func TestValidateArgs_Success(t *testing.T) {
	tests := []struct {
		name        string
		ago         string
		at          string
		description string
		task        string
		now         time.Time
	}{
		{name: "description_at", at: "01:00", description: "organising calendar"},
		{name: "description_task_at", at: "01:00", description: "organising calendar", task: "1.1"},
		{name: "description_ago", ago: "10m", description: "organising calendar"},
		{name: "description_task_ago", ago: "10m", description: "organising calendar", task: "1.5"},
		{name: "task_at", at: "01:00", task: "1.1"},
		{name: "task_ago", ago: "10m", task: "1.1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sa := startArgs{description: tc.description,
				task: tc.task,
				at:   tc.at,
				ago:  tc.ago}
			err := validateFlags(sa, tc.now)

			if err != nil {
				t.Fatalf("got %v, want nil", err)
			}
		})
	}
}
