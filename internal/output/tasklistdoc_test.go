package output

import "testing"

func TestTaskListDoc_Marshal_AllKeysPresentWithValues(t *testing.T) {
	tests := []struct {
		name      string
		truncated bool
	}{
		// false is the zero value, so check it is still written rather than omitted
		{"not_truncated", false},
		{"truncated", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tasks := []TaskItem{{PageID: "3f63b23e", ShortID: "837922", Name: "Write the sync engine"}}
			doc := NewTaskListDoc(tasks, tc.truncated, nil)

			resultsMap := marshalAndDecodeDoc(t, doc)

			if value, ok := resultsMap["schema_version"]; !ok || value != float64(1) {
				t.Errorf("Got schema_version = %v (present: %v), want %v", value, ok, 1)
			}

			if value, ok := resultsMap["truncated"]; !ok || value != tc.truncated {
				t.Errorf("Got truncated = %v (present: %v), want %v", value, ok, tc.truncated)
			}

			if _, ok := resultsMap["warnings"].([]any); !ok {
				t.Errorf("Got warnings = %v, want an array", resultsMap["warnings"])
			}

			if len(resultsMap) != 4 {
				t.Errorf("Got %d keys, want 4: %v", len(resultsMap), resultsMap)
			}
		})
	}
}

func TestTaskListDoc_Marshal_Tasks_AlwaysAnArray(t *testing.T) {
	tests := []struct {
		name    string
		tasks   []TaskItem
		wantLen int
	}{
		{"nil", nil, 0},
		{"empty", []TaskItem{}, 0},
		{"two_tasks", []TaskItem{{PageID: "3f63b23e"}, {PageID: "4a74c34f"}}, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewTaskListDoc(tc.tasks, false, nil)

			resultsMap := marshalAndDecodeDoc(t, doc)

			// A JSON null would decode to a nil any, so this also fails for null
			tasks, ok := resultsMap["tasks"].([]any)
			if !ok {
				t.Fatalf("Got tasks = %v, want an array", resultsMap["tasks"])
			}

			if len(tasks) != tc.wantLen {
				t.Errorf("Got %d tasks, want %d: %v", len(tasks), tc.wantLen, tasks)
			}
		})
	}
}

// The shape of a single task item is covered in taskitem_test.go
func TestTaskListDoc_Marshal_Tasks_ObjectsInOrder(t *testing.T) {
	tasks := []TaskItem{{PageID: "3f63b23e"}, {PageID: "4a74c34f"}}
	doc := NewTaskListDoc(tasks, false, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	gotTasks, ok := resultsMap["tasks"].([]any)
	if !ok {
		t.Fatalf("Got tasks = %v, want an array", resultsMap["tasks"])
	}

	// The list is already in order; the document must not reorder it
	wantIDs := []string{"3f63b23e", "4a74c34f"}
	if len(gotTasks) != len(wantIDs) {
		t.Fatalf("Got %d tasks, want %d: %v", len(gotTasks), len(wantIDs), gotTasks)
	}

	for i, wantID := range wantIDs {
		item, ok := gotTasks[i].(map[string]any)
		if !ok {
			t.Fatalf("Got tasks[%d] = %v, want an object", i, gotTasks[i])
		}

		if value, ok := item["page_id"]; !ok || value != wantID {
			t.Errorf("Got tasks[%d].page_id = %v, want %q", i, value, wantID)
		}
	}
}
