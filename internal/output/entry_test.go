package output

import (
	"encoding/json"
	"testing"
	"time"
)

func marshalAndDecode(t *testing.T, e *Entry) map[string]any {
	t.Helper()
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("unable to marshal test Entry: %v", err)
	}

	result := make(map[string]any)
	unmarshalErr := json.Unmarshal(data, &result)
	if unmarshalErr != nil {
		t.Fatalf("unable to unmarshal Entry JSON: %v", unmarshalErr)
	}

	return result
}

func TestEntry_Marshal_FinishedWithTaskAttached_AllKeysPresent(t *testing.T) {
	startTime := NewUTCTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	endTime := NewUTCTime(time.Date(2026, 1, 1, 0, 30, 10, 0, time.UTC))
	task := &TaskRef{}
	entry := &Entry{
		ID:          "1234",
		Start:       startTime,
		End:         &endTime,
		Minutes:     new(30),
		Seconds:     new(1810),
		Description: new("description string"),
		Task:        task,
		SyncState:   "pending",
		SyncError:   new("fake_error"),
		Deleted:     false,
	}

	resultsMap := marshalAndDecode(t, entry)

	if value, ok := resultsMap["id"]; !ok || value != entry.ID {
		t.Errorf("Got id = %q, want %q", value, entry.ID)
	}

	expectedStart := "2026-01-01T00:00:00Z"
	if value, ok := resultsMap["start"]; !ok || value != expectedStart {
		t.Errorf("Got start = %q, want %q", value, expectedStart)
	}

	expectedEnd := "2026-01-01T00:30:10Z"
	if value, ok := resultsMap["end"]; !ok || value != expectedEnd {
		t.Errorf("Got end = %q, want %q", value, expectedEnd)
	}

	if value, ok := resultsMap["minutes"]; !ok || value != float64(30) {
		t.Errorf("Got minutes = %v, want %v", value, 30)
	}

	if value, ok := resultsMap["seconds"]; !ok || value != float64(1810) {
		t.Errorf("Got seconds = %v, want %v", value, 1810)
	}

	if value, ok := resultsMap["description"]; !ok || value != "description string" {
		t.Errorf("Got description = %q, want %q", value, "description string")
	}

	// only check exists, not nil
	if value, ok := resultsMap["task"]; !ok || value == nil {
		t.Errorf("Got task = %q, want %q", value, task)
	}

	if value, ok := resultsMap["sync_state"]; !ok || value != "pending" {
		t.Errorf("Got sync_state = %q, want %q", value, "pending")
	}

	if value, ok := resultsMap["sync_error"]; !ok || value != "fake_error" {
		t.Errorf("Got sync_error = %q, want %q", value, "fake_error")
	}

	if value, ok := resultsMap["deleted"]; !ok || value != false {
		t.Errorf("Got deleted = %v, want %v", value, false)
	}

	// Guards against extra or renamed keys; the contract (DESIGN.md §8) has exactly ten
	if len(resultsMap) != 10 {
		t.Errorf("Got %d keys, want 10: %v", len(resultsMap), resultsMap)
	}
}

func TestEntry_Marshal_RunningUnattachedEntry_UnsetKeysPresentAndNil(t *testing.T) {
	entry := &Entry{
		ID:        "1234",
		Start:     NewUTCTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		SyncState: "pending",
	}

	resultsMap := marshalAndDecode(t, entry)

	nullKeys := []string{"end", "seconds", "minutes", "description", "task", "sync_error"}
	for _, key := range nullKeys {
		t.Run(key, func(t *testing.T) {
			if value, ok := resultsMap[key]; !ok || value != nil {
				t.Errorf("Got %s = %v (present: %v), want present with value nil", key, value, ok)
			}
		})
	}

	if len(resultsMap) != 10 {
		t.Errorf("Got %d keys, want 10: %v", len(resultsMap), resultsMap)
	}
}

func TestEntry_Marshal_NoTaskAttached_TaskPresentAndNil(t *testing.T) {
	startTime := NewUTCTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	entry := &Entry{
		ID:          "1234",
		Start:       startTime,
		Description: new("Started entry, not finished or attached"),
	}

	resultsMap := marshalAndDecode(t, entry)

	if value, ok := resultsMap["task"]; !ok || value != nil {
		t.Errorf("Got task = %v, want %v", value, nil)
	}
}

func TestEntry_Marshal_TaskAttached_TaskRefMarshals(t *testing.T) {
	startTime := NewUTCTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	endTime := NewUTCTime(time.Date(2026, 1, 1, 0, 30, 10, 0, time.UTC))
	task := &TaskRef{PageID: "4321", ShortID: "21", Name: "task_example"}
	entry := &Entry{
		ID:          "1234",
		Start:       startTime,
		End:         &endTime,
		Minutes:     new(30),
		Seconds:     new(1810),
		Description: new("description string"),
		Task:        task,
		SyncState:   "pending",
		SyncError:   nil,
		Deleted:     false,
	}

	resultsMap := marshalAndDecode(t, entry)

	taskMap, ok := resultsMap["task"].(map[string]any)
	if !ok {
		t.Fatalf("Task not found")
	}

	if value, ok := taskMap["page_id"]; !ok || value != "4321" {
		t.Errorf("Got task.page_id = %q, want %q", value, "4321")
	}

	if value, ok := taskMap["short_id"]; !ok || value != "21" {
		t.Errorf("Got task.short_id = %q, want %q", value, "21")
	}

	if value, ok := taskMap["name"]; !ok || value != "task_example" {
		t.Errorf("Got task.name = %q, want %q", value, "task_example")
	}
}
