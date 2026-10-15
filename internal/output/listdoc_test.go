package output

import "testing"

func TestListDoc_Marshal_AllKeysPresentWithValues(t *testing.T) {
	tests := []struct {
		name         string
		totalMinutes int
		truncated    bool
	}{
		// Zero values must still be written, not omitted
		{"zero_values", 0, false},
		{"set_values", 95, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewListDoc([]Entry{newTestEntry()}, tc.totalMinutes, tc.truncated, nil)

			resultsMap := marshalAndDecodeDoc(t, doc)

			if value, ok := resultsMap["schema_version"]; !ok || value != float64(1) {
				t.Errorf("Got schema_version = %v (present: %v), want %v", value, ok, 1)
			}

			if value, ok := resultsMap["total_minutes"]; !ok || value != float64(tc.totalMinutes) {
				t.Errorf("Got total_minutes = %v (present: %v), want %v", value, ok, tc.totalMinutes)
			}

			if value, ok := resultsMap["truncated"]; !ok || value != tc.truncated {
				t.Errorf("Got truncated = %v (present: %v), want %v", value, ok, tc.truncated)
			}

			if _, ok := resultsMap["warnings"].([]any); !ok {
				t.Errorf("Got warnings = %v, want an array", resultsMap["warnings"])
			}

			if len(resultsMap) != 5 {
				t.Errorf("Got %d keys, want 5: %v", len(resultsMap), resultsMap)
			}
		})
	}
}

func TestListDoc_Marshal_Entries_AlwaysAnArray(t *testing.T) {
	tests := []struct {
		name    string
		entries []Entry
		wantLen int
	}{
		{"nil", nil, 0},
		{"empty", []Entry{}, 0},
		{"two_entries", []Entry{newTestEntry(), newTestEntry()}, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewListDoc(tc.entries, 0, false, nil)

			resultsMap := marshalAndDecodeDoc(t, doc)

			// A JSON null would decode to a nil any, so this also fails for null
			entries, ok := resultsMap["entries"].([]any)
			if !ok {
				t.Fatalf("Got entries = %v, want an array", resultsMap["entries"])
			}

			if len(entries) != tc.wantLen {
				t.Errorf("Got %d entries, want %d: %v", len(entries), tc.wantLen, entries)
			}
		})
	}
}

func TestListDoc_Marshal_Entries_PlainEntryObjectsInOrder(t *testing.T) {
	first := newTestEntry()
	second := newTestEntry()
	second.ID = "5678"

	doc := NewListDoc([]Entry{first, second}, 0, false, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	entries, ok := resultsMap["entries"].([]any)
	if !ok {
		t.Fatalf("Got entries = %v, want an array", resultsMap["entries"])
	}

	// The list is already in order; the document must not reorder it
	wantIDs := []string{"1234", "5678"}
	if len(entries) != len(wantIDs) {
		t.Fatalf("Got %d entries, want %d: %v", len(entries), len(wantIDs), entries)
	}

	for i, wantID := range wantIDs {
		// Each item decodes as its own any, so assert it to an object first
		item, ok := entries[i].(map[string]any)
		if !ok {
			t.Fatalf("Got entries[%d] = %v, want an object", i, entries[i])
		}

		if value, ok := item["id"]; !ok || value != wantID {
			t.Errorf("Got entries[%d].id = %v, want %q", i, value, wantID)
		}

		// A wrapped document here would mean items are EntryDocs rather than Entries
		for _, key := range []string{"schema_version", "entry", "warnings"} {
			if _, found := item[key]; found {
				t.Errorf("entries[%d] should not have a %s key", i, key)
			}
		}
	}
}
