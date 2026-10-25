package cli

import (
	"errors"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestValidateArgs_Error(t *testing.T) {
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
			_, err := validateFlags(sa, tc.now, tc.loc)
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
			_, err := validateFlags(sa, tc.now, tc.loc)

			if err != nil {
				t.Fatalf("got %v, want nil", err)
			}
		})
	}
}
