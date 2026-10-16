package output

import (
	"encoding/json"
	"testing"
)

func marshalAndDecodeWarning(t *testing.T, w Warning) map[string]any {
	t.Helper()
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatalf("unable to marshal test Warning: %v", err)
	}

	result := make(map[string]any)
	unmarshalErr := json.Unmarshal(data, &result)
	if unmarshalErr != nil {
		t.Fatalf("unable to unmarshal Warning JSON: %v", unmarshalErr)
	}

	return result
}

func TestWarning_Marshal_NilDetails_DetailsPresentAndNil(t *testing.T) {
	warning := Warning{Code: "stale_timer", Message: "timer started on an earlier day"}

	resultsMap := marshalAndDecodeWarning(t, warning)

	if value, ok := resultsMap["code"]; !ok || value != "stale_timer" {
		t.Errorf("Got code = %v, want %q", value, "stale_timer")
	}

	if value, ok := resultsMap["message"]; !ok || value != "timer started on an earlier day" {
		t.Errorf("Got message = %v, want %q", value, "timer started on an earlier day")
	}

	if value, ok := resultsMap["details"]; !ok || value != nil {
		t.Errorf("Got details = %v (present: %v), want present with value nil", value, ok)
	}

	if len(resultsMap) != 3 {
		t.Errorf("Got %d keys, want 3: %v", len(resultsMap), resultsMap)
	}
}

func TestWarning_Marshal_WithDetails_DetailsMarshalsAsObject(t *testing.T) {
	warning := Warning{
		Code:    "overlap",
		Message: "entry overlaps an existing entry",
		Details: map[string]any{"entry_id": "abc"},
	}

	resultsMap := marshalAndDecodeWarning(t, warning)

	details, ok := resultsMap["details"].(map[string]any)
	if !ok {
		t.Fatalf("Got details = %v, want an object", resultsMap["details"])
	}

	if value, ok := details["entry_id"]; !ok || value != "abc" {
		t.Errorf("Got details.entry_id = %v, want %q", value, "abc")
	}

	if len(resultsMap) != 3 {
		t.Errorf("Got %d keys, want 3: %v", len(resultsMap), resultsMap)
	}
}
