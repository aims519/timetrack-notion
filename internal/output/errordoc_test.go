package output

import (
	"bytes"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestErrorDoc_Marshal_KeysPresentAndCorrect(t *testing.T) {
	tests := []struct {
		name string
		err  clierr.Error
	}{
		{"not_found", clierr.Error{Code: clierr.NotFound, Message: "no task matches \"999\"", Details: map[string]any{"selector": "999"}}},
		{"nil_details", clierr.Error{Code: clierr.NoTimerRunning, Message: "no timer running", Details: nil}},
		{"with_cause", clierr.Error{Code: clierr.NoTimerRunning, Message: "no timer running", Details: nil, Cause: errors.New("underlying cause")}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewErrorDoc(tc.err)
			resultsMap := marshalAndDecodeDoc(t, doc)

			if len(resultsMap) != 2 {
				t.Fatalf("got %v ErrorDoc keys, want 2", len(resultsMap))
			}

			if value, ok := resultsMap["schema_version"]; !ok || value != float64(SchemaVersion) {
				t.Errorf("got ErrorDoc.SchemaVersion = %v, want %v", value, SchemaVersion)
			}

			errorMap, ok := resultsMap["error"].(map[string]any)
			if !ok {
				t.Fatalf("got ErrorDoc.Error = %v, want an object", resultsMap["error"])
			}

			if len(errorMap) != 3 {
				t.Fatalf("got %v ErrorDoc.Error keys, want 3", len(errorMap))
			}

			if value, ok := errorMap["code"]; !ok || value != string(tc.err.Code) {
				t.Errorf("got ErrorDoc.Error.Code = %v, want %v", value, tc.err.Code)
			}

			if value, ok := errorMap["message"]; !ok || value != tc.err.Message {
				t.Errorf("got ErrorDoc.Error.Message = %v, want %v", value, tc.err.Message)
			}

			// Cause always absent
			if value, found := errorMap["cause"]; found {
				t.Errorf("ErrorDoc.Error.Cause should always be absent, got %v", value)
			}

			// Stop here if Details == nil, since it can't be asserted as an object
			if tc.err.Details == nil {
				if value, ok := errorMap["details"]; !ok || value != nil {
					t.Errorf("got ErrorDoc.Error.Details = %v, want nil", errorMap["details"])
				}
				return
			}

			detailsMap, ok := errorMap["details"].(map[string]any)
			if !ok {
				t.Fatalf("got ErrorDoc.Error.Details = %v, want an object", errorMap["details"])
			}

			if !maps.Equal(detailsMap, tc.err.Details) {
				t.Errorf("Got ErrorDoc.Error.Details = %v, want %v", detailsMap, tc.err.Details)
			}
		})
	}
}

func TestErrorDoc_WriteTable_WritesExpectedOutput(t *testing.T) {
	doc := ErrorDoc{Error: clierr.Error{Message: "expected message"}}
	w := &bytes.Buffer{}

	err := doc.WriteTable(w, time.UTC)
	if err != nil {
		t.Fatalf("WriteTable() error = %v", err)
	}

	got := w.String()
	want := "expected message\n"

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
