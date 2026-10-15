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

// marshalAndDecodeDoc works for any document type, since json.Marshal takes any
func marshalAndDecodeDoc(t *testing.T, doc any) map[string]any {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("unable to marshal test document: %v", err)
	}

	result := make(map[string]any)
	unmarshalErr := json.Unmarshal(data, &result)
	if unmarshalErr != nil {
		t.Fatalf("unable to unmarshal document JSON: %v", unmarshalErr)
	}

	return result
}

func TestStartDoc_Marshal_PlainStart_StoppedPresentAndNil(t *testing.T) {
	doc := NewStartDoc(newTestEntry(), nil, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	if value, ok := resultsMap["schema_version"]; !ok || value != float64(1) {
		t.Errorf("Got schema_version = %v (present: %v), want %v", value, ok, 1)
	}

	if _, ok := resultsMap["entry"].(map[string]any); !ok {
		t.Errorf("Got entry = %v, want an object", resultsMap["entry"])
	}

	if value, ok := resultsMap["stopped"]; !ok || value != nil {
		t.Errorf("Got stopped = %v (present: %v), want present with value nil", value, ok)
	}

	if _, ok := resultsMap["warnings"].([]any); !ok {
		t.Errorf("Got warnings = %v, want an array", resultsMap["warnings"])
	}

	if len(resultsMap) != 4 {
		t.Errorf("Got %d keys, want 4: %v", len(resultsMap), resultsMap)
	}
}

func TestStartDoc_Marshal_Switch_StoppedIsTheStoppedEntry(t *testing.T) {
	stopped := newTestEntry()
	stopped.ID = "5678"

	doc := NewStartDoc(newTestEntry(), &stopped, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	stoppedMap, ok := resultsMap["stopped"].(map[string]any)
	if !ok {
		t.Fatalf("Got stopped = %v, want an object", resultsMap["stopped"])
	}

	// Distinct IDs catch entry and stopped being swapped
	if value, ok := stoppedMap["id"]; !ok || value != "5678" {
		t.Errorf("Got stopped.id = %v, want %q", value, "5678")
	}

	entryMap, ok := resultsMap["entry"].(map[string]any)
	if !ok {
		t.Fatalf("Got entry = %v, want an object", resultsMap["entry"])
	}

	if value, ok := entryMap["id"]; !ok || value != "1234" {
		t.Errorf("Got entry.id = %v, want %q", value, "1234")
	}

	if len(resultsMap) != 4 {
		t.Errorf("Got %d keys, want 4: %v", len(resultsMap), resultsMap)
	}
}

// The value of removed is covered separately; this checks the other keys
func TestDeleteDoc_Marshal_AllKeysPresentWithValues(t *testing.T) {
	tests := []struct {
		name   string
		dryRun bool
		action DeleteAction
	}{
		{"dry_run_tombstone", true, DeleteActionTombstone},
		{"real_remove", false, DeleteActionRemove},
		{"real_tombstone", false, DeleteActionTombstone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewDeleteDoc(tc.dryRun, tc.action, newTestEntry(), nil)

			resultsMap := marshalAndDecodeDoc(t, doc)

			if value, ok := resultsMap["schema_version"]; !ok || value != float64(1) {
				t.Errorf("Got schema_version = %v (present: %v), want %v", value, ok, 1)
			}

			if value, ok := resultsMap["dry_run"]; !ok || value != tc.dryRun {
				t.Errorf("Got dry_run = %v (present: %v), want %v", value, ok, tc.dryRun)
			}

			// The decoded value is a plain string, so convert before comparing
			if value, ok := resultsMap["action"]; !ok || value != string(tc.action) {
				t.Errorf("Got action = %v (present: %v), want %q", value, ok, tc.action)
			}

			if _, ok := resultsMap["entry"].(map[string]any); !ok {
				t.Errorf("Got entry = %v, want an object", resultsMap["entry"])
			}

			if _, ok := resultsMap["warnings"].([]any); !ok {
				t.Errorf("Got warnings = %v, want an array", resultsMap["warnings"])
			}

			if len(resultsMap) != 6 {
				t.Errorf("Got %d keys, want 6: %v", len(resultsMap), resultsMap)
			}
		})
	}
}

func TestNewDeleteDoc_Marshal_RemovedFlagCorrectlySet(t *testing.T) {
	tests := []struct {
		name   string
		dryRun bool
		action DeleteAction
		want   any
	}{
		{"dryrun_remove", true, DeleteActionRemove, nil},
		{"dryrun_tombstone", true, DeleteActionTombstone, nil},
		{"notdryrun_remove", false, DeleteActionRemove, true},
		{"notdryrun_tombstone", false, DeleteActionTombstone, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deleteDoc := NewDeleteDoc(tc.dryRun, tc.action, newTestEntry(), []Warning{})
			resultsMap := marshalAndDecodeDoc(t, deleteDoc)

			if value, ok := resultsMap["removed"]; !ok || value != tc.want {
				t.Errorf("Got DeleteDoc.Removed = %v, want %v", value, tc.want)
			}
		})
	}
}
