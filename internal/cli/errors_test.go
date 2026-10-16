package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
	"github.com/aims519/timetrack-notion/internal/output"
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

func TestPrintError_WhenCalled_WritesExpectedOutput(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		format      output.Format
		wantCode    clierr.ErrorCode
		wantMessage string
	}{
		{"json_clierr", &clierr.Error{Code: clierr.AccessDenied, Message: "access denied"}, output.JSON, clierr.AccessDenied, "access denied"},
		{"table_clierr", &clierr.Error{Code: clierr.AccessDenied, Message: "access denied"}, output.Table, clierr.AccessDenied, "access denied"},
		{"table_plain_error", errors.New("boom"), output.Table, clierr.Internal, "boom"},
		{"json_plain_error", errors.New("boom"), output.JSON, clierr.Internal, "boom"},
		{"table_wrapped_error", fmt.Errorf("wrapping: %w", &clierr.Error{Code: clierr.NotFound, Message: "not found"}), output.Table, clierr.NotFound, "not found"},
		{"json_wrapped_error", fmt.Errorf("wrapping: %w", &clierr.Error{Code: clierr.NotFound, Message: "not found"}), output.JSON, clierr.NotFound, "not found"},
		{"json_unmarshalable_error", &clierr.Error{Code: clierr.AccessDenied, Message: "access denied", Details: map[string]any{"x": math.NaN()}}, output.JSON, clierr.AccessDenied, "access denied"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := &bytes.Buffer{}
			printError(b, tc.format, tc.err)

			if b.Len() == 0 {
				t.Fatalf("nothing written to %T", b)
			}

			// Table should be the error message and newline
			if tc.format == output.Table {
				if got := b.String(); got != tc.wantMessage+"\n" {
					t.Errorf("got %q, want %q", got, tc.wantMessage+"\n")
				}
			}

			// JSON output should have the correct code and message
			if tc.format == output.JSON {
				var got output.ErrorDoc
				if err := json.Unmarshal(b.Bytes(), &got); err != nil {
					t.Fatalf("json.Unmarshal() error = %v", err)
				}

				if got.Error.Code != tc.wantCode {
					t.Errorf("got code %q, want %q", got.Error.Code, tc.wantCode)
				}
				if got.Error.Message != tc.wantMessage {
					t.Errorf("got message %q, want %q", got.Error.Message, tc.wantMessage)
				}
			}
		})
	}
}
