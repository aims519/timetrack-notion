package output

import (
	"encoding/json"
	"testing"
)

func marshalAndDecodeTaskItem(t *testing.T, item TaskItem) map[string]any {
	t.Helper()
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("unable to marshal test TaskItem: %v", err)
	}

	result := make(map[string]any)
	unmarshalErr := json.Unmarshal(data, &result)
	if unmarshalErr != nil {
		t.Fatalf("unable to unmarshal TaskItem JSON: %v", unmarshalErr)
	}

	return result
}

func TestTaskItem_Marshal_AllFieldsSet_AllKeysPresentWithValues(t *testing.T) {
	item := TaskItem{
		PageID:  "3f63b23e",
		ShortID: "837922",
		Order:   new(2.3),
		Name:    "Learn: error wrapping",
		Project: &ProjectRef{PageID: "3e73b23e", Name: "Go Time Tracker"},
		Done:    true,
	}

	resultsMap := marshalAndDecodeTaskItem(t, item)

	if value, ok := resultsMap["page_id"]; !ok || value != "3f63b23e" {
		t.Errorf("Got page_id = %v, want %q", value, "3f63b23e")
	}

	if value, ok := resultsMap["short_id"]; !ok || value != "837922" {
		t.Errorf("Got short_id = %v, want %q", value, "837922")
	}

	// JSON numbers decode into map[string]any as float64
	if value, ok := resultsMap["order"]; !ok || value != 2.3 {
		t.Errorf("Got order = %v, want %v", value, 2.3)
	}

	if value, ok := resultsMap["name"]; !ok || value != "Learn: error wrapping" {
		t.Errorf("Got name = %v, want %q", value, "Learn: error wrapping")
	}

	if value, ok := resultsMap["done"]; !ok || value != true {
		t.Errorf("Got done = %v, want %v", value, true)
	}

	project, ok := resultsMap["project"].(map[string]any)
	if !ok {
		t.Fatalf("Got project = %v, want an object", resultsMap["project"])
	}

	if value, ok := project["page_id"]; !ok || value != "3e73b23e" {
		t.Errorf("Got project.page_id = %v, want %q", value, "3e73b23e")
	}

	if value, ok := project["name"]; !ok || value != "Go Time Tracker" {
		t.Errorf("Got project.name = %v, want %q", value, "Go Time Tracker")
	}

	if len(project) != 2 {
		t.Errorf("Got %d project keys, want 2: %v", len(project), project)
	}

	if len(resultsMap) != 6 {
		t.Errorf("Got %d keys, want 6: %v", len(resultsMap), resultsMap)
	}
}

func TestTaskItem_Marshal_NoProject_ProjectPresentAndNil(t *testing.T) {
	item := TaskItem{PageID: "3f63b23e", ShortID: "837922", Name: "No project task"}

	resultsMap := marshalAndDecodeTaskItem(t, item)

	if value, ok := resultsMap["project"]; !ok || value != nil {
		t.Errorf("Got project = %v (present: %v), want present with value nil", value, ok)
	}

	// An unset bool is false, which must still be written
	if value, ok := resultsMap["done"]; !ok || value != false {
		t.Errorf("Got done = %v (present: %v), want %v", value, ok, false)
	}

	if len(resultsMap) != 6 {
		t.Errorf("Got %d keys, want 6: %v", len(resultsMap), resultsMap)
	}
}

func TestTaskItem_Marshal_Order_NullOnlyWhenUnset(t *testing.T) {
	tests := []struct {
		name  string
		order *float64
		want  any
	}{
		{"unset", nil, nil},
		// A real zero is different from "unset" and must not become null
		{"zero", new(0.0), float64(0)},
		{"fractional", new(2.3), 2.3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			item := TaskItem{PageID: "3f63b23e", ShortID: "837922", Order: tc.order}

			resultsMap := marshalAndDecodeTaskItem(t, item)

			if value, ok := resultsMap["order"]; !ok || value != tc.want {
				t.Errorf("Got order = %v (present: %v), want %v", value, ok, tc.want)
			}
		})
	}
}
