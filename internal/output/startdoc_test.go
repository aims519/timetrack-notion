package output

import "testing"

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
