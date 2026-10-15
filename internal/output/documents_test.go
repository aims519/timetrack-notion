package output

import (
	"encoding/json"
	"testing"
	"time"
)

func marshalAndDecodeEntryDoc(t *testing.T, doc EntryDoc) map[string]any {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("unable to marshal test EntryDoc: %v", err)
	}

	result := make(map[string]any)
	unmarshalErr := json.Unmarshal(data, &result)
	if unmarshalErr != nil {
		t.Fatalf("unable to unmarshal EntryDoc JSON: %v", unmarshalErr)
	}

	return result
}

func newTestEntry() Entry {
	return Entry{
		ID:        "1234",
		Start:     NewUTCTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		SyncState: "pending",
	}
}

func TestEntryDoc_Marshal_EmbeddedFieldsFlattenedToTopLevel(t *testing.T) {
	doc := NewEntryDoc(newTestEntry(), nil)

	resultsMap := marshalAndDecodeEntryDoc(t, doc)

	// JSON numbers decode into map[string]any as float64
	if value, ok := resultsMap["schema_version"]; !ok || value != float64(1) {
		t.Errorf("Got schema_version = %v (present: %v), want %v", value, ok, 1)
	}

	entry, ok := resultsMap["entry"].(map[string]any)
	if !ok {
		t.Fatalf("Got entry = %v, want an object", resultsMap["entry"])
	}

	if value, ok := entry["id"]; !ok || value != "1234" {
		t.Errorf("Got entry.id = %v, want %q", value, "1234")
	}

	// A nested BaseSchema/BaseWarnings key would mean the embedding is not flattened
	for _, key := range []string{"BaseSchema", "BaseWarnings"} {
		if _, found := resultsMap[key]; found {
			t.Errorf("%s key should not be present", key)
		}
	}

	if len(resultsMap) != 3 {
		t.Errorf("Got %d keys, want 3: %v", len(resultsMap), resultsMap)
	}
}

func TestEntryDoc_Marshal_Warnings_AlwaysAnArray(t *testing.T) {
	tests := []struct {
		name     string
		warnings []Warning
		wantLen  int
	}{
		{"nil", nil, 0},
		{"empty", []Warning{}, 0},
		{"one_warning", []Warning{{Code: Overlap, Message: "overlaps"}}, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewEntryDoc(newTestEntry(), tc.warnings)

			resultsMap := marshalAndDecodeEntryDoc(t, doc)

			// A JSON null would decode to a nil any, so this also fails for null
			warnings, ok := resultsMap["warnings"].([]any)
			if !ok {
				t.Fatalf("Got warnings = %v, want an array", resultsMap["warnings"])
			}

			if len(warnings) != tc.wantLen {
				t.Errorf("Got %d warnings, want %d: %v", len(warnings), tc.wantLen, warnings)
			}
		})
	}
}
