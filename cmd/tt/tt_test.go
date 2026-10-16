package main

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestExitCode_CLIErrorsMappedToExitCodes(t *testing.T) {

	wrappedTimerError := fmt.Errorf("wrapped error: %w", &clierr.Error{Code: clierr.TimerRunning})
	reWrappedTimerError := fmt.Errorf("rewrapped error: %w", wrappedTimerError)

	notFoundError := &clierr.Error{Code: clierr.NotFound}                                      // 3
	offlineErrorWithNotFoundCause := &clierr.Error{Cause: notFoundError, Code: clierr.Offline} // 6

	tests := []struct {
		name     string
		err      error
		exitCode int
	}{
		{"direct", &clierr.Error{Code: clierr.TimerRunning}, 4},
		{"wrapped", wrappedTimerError, 4},
		{"wrapped_twice", reWrappedTimerError, 4},
		{"plain_error", errors.New("boom"), 10},
		{"unknown_error_code", &clierr.Error{Code: "nonsense"}, 10},
		{"empty_code", &clierr.Error{}, 10},
		{"nested_clierr", offlineErrorWithNotFoundCause, 6},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := exitCode(tc.err)
			if got != tc.exitCode {
				t.Errorf("%s: exitCode(%v) = %v, want %v", tc.name, tc.err, got, tc.exitCode)
			}
		})
	}
}
