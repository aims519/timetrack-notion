package clierr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestExitCode_ReturnsExpectedCodes(t *testing.T) {
	// Must be kept up-to-date to ensure all error codes have corresponding exit codes defined
	tests := []struct {
		code     ErrorCode
		exitCode int
	}{
		{SyncPartial, 1},
		{Usage, 2},
		{InvalidTime, 2},
		{ConfirmationRequired, 2},
		{Cancelled, 2},
		{NotFound, 3},
		{AmbiguousSelector, 3},
		{TimerRunning, 4},
		{NoTimerRunning, 4},
		{SyncBusy, 4},
		{NotInitialised, 5},
		{NoToken, 5},
		{AccessDenied, 5},
		{SchemaMismatch, 5},
		{DatabaseTooNew, 5},
		{DatabaseUnusable, 5},
		{Offline, 6},
		{Internal, 10},

		// Edge cases
		{"unknown_case", 10},
		{"", 10},
	}

	for _, tc := range tests {
		t.Run(string(tc.code), func(t *testing.T) {
			got := tc.code.ExitCode()
			if got != tc.exitCode {
				t.Errorf("%v.ExitCode() = %v, want %v", tc.code, got, tc.exitCode)
			}
		})
	}
}

func TestError_CanIdentifyUnderlyingCause(t *testing.T) {
	cliErr := &Error{Cause: os.ErrNotExist}
	if !errors.Is(cliErr, os.ErrNotExist) {
		t.Errorf("could not identify ErrNotExist as the underlying cause due to possible missing Unwrap()")
	}
}

func TestError_WhenWrapped_CanIdentifyUnderlyingCause(t *testing.T) {
	cliErr := &Error{Cause: os.ErrNotExist}
	wrappedError := fmt.Errorf("higher error: %w", cliErr)

	var ce *Error
	if !errors.As(wrappedError, &ce) {
		t.Fatalf("possible missing Unwrap(), couldn't find *Error")
	}

	if ce != cliErr {
		t.Errorf("unwrapped error, %v, was not of expected type", ce)
	}

	if !errors.Is(wrappedError, os.ErrNotExist) {
		t.Errorf("underlying cause not present")
	}
}

func TestUnwrap_NoCause_ReturnsNil(t *testing.T) {
	err := &Error{}

	cause := errors.Unwrap(err)
	if cause != nil {
		t.Errorf("Got %q, want %v", cause, nil)
	}
}

func TestError_WithoutCause_ReturnsMessageOnly(t *testing.T) {
	expectedMessage := "the message"
	err := &Error{Message: expectedMessage}

	result := err.Error()
	if result != expectedMessage {
		t.Errorf("Error() = %q, want %q", result, expectedMessage)
	}
}

func TestError_WithCause_ReturnsMessageWithCauseString(t *testing.T) {
	cause := errors.New("underlying")
	errorMessage := "error message"

	err := &Error{Message: errorMessage, Cause: cause}
	expectedMessage := "error message: underlying"

	result := err.Error()
	if result != expectedMessage {
		t.Errorf("Error() = %q, want %q", result, expectedMessage)
	}
}

func marshalAndDecode(t *testing.T, e *Error) map[string]any {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("unable to marshal test Error: %v", err)
	}

	result := make(map[string]any)
	unmarshalErr := json.Unmarshal(data, &result)
	if unmarshalErr != nil {
		t.Fatalf("unable to unmarshal error JSON: %v", unmarshalErr)
	}

	return result
}

// Checks if details in the marshaled JSON is present with value nil
func TestError_Marshal_NilDetails_WritesNull(t *testing.T) {
	testErr := &Error{Code: DatabaseUnusable, Message: "any message"}

	check := marshalAndDecode(t, testErr)

	if value, ok := check["details"]; !ok || value != nil {
		t.Errorf("Got details = %v, want nil", value)
	}
}

func TestError_Marshal_WithCause_OmitsCauseKey(t *testing.T) {
	testErr := &Error{Code: DatabaseUnusable, Message: "any message", Cause: os.ErrNotExist}

	check := marshalAndDecode(t, testErr)

	if _, found := check["cause"]; found {
		t.Errorf("cause key should be omitted")
	}

	if _, found := check["Cause"]; found {
		t.Errorf("Cause key should be omitted")
	}
}
