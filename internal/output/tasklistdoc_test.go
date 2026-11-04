package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

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

func TestTaskListDoc_WriteTable_WritesExpectedOutput(t *testing.T) {
	tests := []struct {
		name  string
		tasks []TaskItem
		want  []string // lines, without their trailing newlines
	}{
		{"no_tasks", nil, []string{"No tasks found"}},
		{
			// Columns are padded to their widest cell plus 3 spaces; the last column isn't padded
			name: "columns_aligned",
			tasks: []TaskItem{
				{ShortID: "837922", Order: ptr(2.3), Name: "Write the sync engine"},
				{ShortID: "a1b2c3d", Order: ptr(10.0), Name: "Plan", Done: true},
			},
			want: []string{
				"Order   ShortID   Name                    Done",
				"2.3     837922    Write the sync engine   false",
				"10      a1b2c3d   Plan                    true",
			},
		},
		{
			// Orders are shown as Notion shows them: no trailing zeros, and - when unset
			name: "order_formatting",
			tasks: []TaskItem{
				{ShortID: "aaaaaa", Order: ptr(4.0), Name: "Whole"},
				{ShortID: "bbbbbb", Order: ptr(4.10), Name: "Trailing zero"},
				{ShortID: "cccccc", Order: ptr(2.25), Name: "Two places"},
				{ShortID: "dddddd", Order: ptr(-1.5), Name: "Negative"},
				{ShortID: "eeeeee", Name: "Unordered"},
			},
			want: []string{
				"Order   ShortID   Name            Done",
				"4       aaaaaa    Whole           false",
				"4.1     bbbbbb    Trailing zero   false",
				"2.25    cccccc    Two places      false",
				"-1.5    dddddd    Negative        false",
				"-       eeeeee    Unordered       false",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewTaskListDoc(tc.tasks, false, nil)
			w := &bytes.Buffer{}

			err := doc.WriteTable(w, time.UTC)
			if err != nil {
				t.Fatalf("WriteTable() error = %v", err)
			}

			if got, want := w.String(), strings.Join(tc.want, "\n")+"\n"; got != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestTaskListDoc_WriteTable_WarningsNotInTable(t *testing.T) {
	warnings := []Warning{{Code: "test_warning", Message: "something to warn about"}}
	doc := NewTaskListDoc([]TaskItem{{ShortID: "837922", Name: "Write the sync engine"}}, false, warnings)
	w := &bytes.Buffer{}

	err := doc.WriteTable(w, time.UTC)
	if err != nil {
		t.Fatalf("WriteTable() error = %v", err)
	}

	// Write prints warnings to stderr, so the table must not repeat them (DESIGN.md §8)
	if strings.Contains(w.String(), "something to warn about") {
		t.Errorf("table output want no warning, got %q", w.String())
	}
}
