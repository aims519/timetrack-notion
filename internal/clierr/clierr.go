package clierr

import (
	"fmt"
)

type ErrorCode string

const (
	SyncPartial          ErrorCode = "sync_partial"
	Usage                ErrorCode = "usage"
	InvalidTime          ErrorCode = "invalid_time"
	ConfirmationRequired ErrorCode = "confirmation_required"
	Cancelled            ErrorCode = "cancelled"
	NotFound             ErrorCode = "not_found"
	AmbiguousSelector    ErrorCode = "ambiguous_selector"
	TimerRunning         ErrorCode = "timer_running"
	NoTimerRunning       ErrorCode = "no_timer_running"
	SyncBusy             ErrorCode = "sync_busy"
	NotInitialised       ErrorCode = "not_initialised"
	NoToken              ErrorCode = "no_token"
	AccessDenied         ErrorCode = "access_denied"
	SchemaMismatch       ErrorCode = "schema_mismatch"
	DatabaseTooNew       ErrorCode = "database_too_new"
	DatabaseUnusable     ErrorCode = "database_unusable"
	Offline              ErrorCode = "offline"
	Internal             ErrorCode = "internal"
)

const internalErrorExitCode = 10

var errorCodeExits = map[ErrorCode]int{
	// Partial failure
	SyncPartial: 1,

	// Usage error
	Usage:                2,
	InvalidTime:          2,
	ConfirmationRequired: 2,
	Cancelled:            2,

	// Not found or ambiguous selector
	NotFound:          3,
	AmbiguousSelector: 3,

	// State conflict
	TimerRunning:   4,
	NoTimerRunning: 4,
	SyncBusy:       4,

	// Config, auth or local store problem
	NotInitialised:   5,
	NoToken:          5,
	AccessDenied:     5,
	SchemaMismatch:   5,
	DatabaseTooNew:   5,
	DatabaseUnusable: 5,

	// Notion unreachable
	Offline: 6,

	// Unexpected internal errors
	Internal: internalErrorExitCode,
}

func (ec ErrorCode) ExitCode() int {
	if v, ok := errorCodeExits[ec]; ok {
		return v
	}
	return internalErrorExitCode // Missing exit code mapping returns as an internal error
}

type Error struct {
	Code    ErrorCode      `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"` // Can be nil, don't need a pointer
	Cause   error          `json:"-"`
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%v: %v", e.Message, e.Cause)
	}

	return e.Message
}

func (e *Error) Unwrap() error {
	return e.Cause
}
