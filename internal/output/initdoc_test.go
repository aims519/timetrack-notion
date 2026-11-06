package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func newTestNotionContext(defaultProject *string) NotionContext {
	tasks, projects, entries := "tasks-ds", "projects-ds", "entries-ds"

	return NotionContext{
		ContextID:            "context-1",
		TasksDataSourceID:    &tasks,
		ProjectsDataSourceID: &projects,
		EntriesDataSourceID:  &entries,
		DefaultProjectPageID: defaultProject,
		PropertyMap:          map[string]string{"project": "Project"},
	}
}

func TestInitDoc_Marshal_FixedKeys(t *testing.T) {
	// Every key is there even when it has nothing to report: 0 waiting, no warnings, no default project
	doc := NewInitDoc(newTestNotionContext(nil), 0, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	if value, ok := resultsMap["schema_version"]; !ok || value != float64(1) {
		t.Errorf("Got schema_version = %v (present: %v), want %v", value, ok, 1)
	}

	// 0 is the zero value, so check it is still written rather than omitted
	if value, ok := resultsMap["waiting"]; !ok || value != float64(0) {
		t.Errorf("Got waiting = %v (present: %v), want %v", value, ok, 0)
	}

	if warnings, ok := resultsMap["warnings"].([]any); !ok || len(warnings) != 0 {
		t.Errorf("Got warnings = %v, want an empty array", resultsMap["warnings"])
	}

	contextMap, ok := resultsMap["context"].(map[string]any)
	if !ok {
		t.Fatalf("Got context = %v, want an object", resultsMap["context"])
	}
	if value, ok := contextMap["default_project_page_id"]; !ok || value != nil {
		t.Errorf("Got context.default_project_page_id = %v (present: %v), want present with value nil", value, ok)
	}

	if len(resultsMap) != 5 {
		t.Errorf("Got %d keys, want 5: %v", len(resultsMap), resultsMap)
	}
}

func TestInitDoc_Marshal_Values(t *testing.T) {
	defaultProject := "project-page"
	doc := NewInitDoc(newTestNotionContext(&defaultProject), 3, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	if value, ok := resultsMap["waiting"]; !ok || value != float64(3) {
		t.Errorf("Got waiting = %v (present: %v), want %v", value, ok, 3)
	}

	contextMap, ok := resultsMap["context"].(map[string]any)
	if !ok {
		t.Fatalf("Got context = %v, want an object", resultsMap["context"])
	}

	// Distinct values catch fields being swapped or written under the wrong key
	tests := []struct {
		key  string
		want string
	}{
		{"context_id", "context-1"},
		{"tasks_data_source_id", "tasks-ds"},
		{"projects_data_source_id", "projects-ds"},
		{"entries_data_source_id", "entries-ds"},
		{"default_project_page_id", "project-page"},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			if value, ok := contextMap[tc.key]; !ok || value != tc.want {
				t.Errorf("Got context.%s = %v (present: %v), want %q", tc.key, value, ok, tc.want)
			}
		})
	}

	propertyMap, ok := contextMap["property_map"].(map[string]any)
	if !ok {
		t.Fatalf("Got context.property_map = %v, want an object", contextMap["property_map"])
	}
	if value := propertyMap["project"]; value != "Project" {
		t.Errorf("Got context.property_map.project = %v, want %q", value, "Project")
	}

	if len(contextMap) != 6 {
		t.Errorf("Got %d context keys, want 6: %v", len(contextMap), contextMap)
	}
}

// Nothing records whether the rollup has been added, so the steps are in every document (DESIGN.md §5)
func TestInitDoc_Marshal_RollupSteps(t *testing.T) {
	doc := NewInitDoc(newTestNotionContext(nil), 0, nil)
	if len(doc.RollupSteps) == 0 {
		t.Fatal("RollupSteps want some steps, got none")
	}

	resultsMap := marshalAndDecodeDoc(t, doc)

	got, ok := resultsMap["rollup_steps"].([]any)
	if !ok {
		t.Fatalf("Got rollup_steps = %v, want an array", resultsMap["rollup_steps"])
	}
	if len(got) != len(doc.RollupSteps) {
		t.Fatalf("Got %d rollup_steps, want %d", len(got), len(doc.RollupSteps))
	}
	for i, step := range doc.RollupSteps {
		if got[i] != step {
			t.Errorf("Got rollup_steps[%d] = %v, want %q", i, got[i], step)
		}
	}
}

func TestInitDoc_WriteTable_WritesExpectedOutput(t *testing.T) {
	defaultProject := "project-page"

	// The first column is as wide as its longest label plus 3 spaces of padding
	const withoutDefault = `Connected to Notion
Tasks             tasks-ds
Projects          projects-ds
Time entries      entries-ds
Default project   none
`
	const withDefault = `Connected to Notion
Tasks             tasks-ds
Projects          projects-ds
Time entries      entries-ds
Default project   project-page
`

	// Only the start of the waiting line is checked, so its wording can change
	tests := []struct {
		name           string
		defaultProject *string
		waiting        int
		wantTable      string
		wantWaiting    string
	}{
		{"no_default_project_nothing_waiting", nil, 0, withoutDefault, ""},
		{"default_project", &defaultProject, 0, withDefault, ""},
		{"one_waiting", nil, 1, withoutDefault, "1 entry waiting"},
		{"several_waiting", nil, 3, withoutDefault, "3 entries waiting"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewInitDoc(newTestNotionContext(tc.defaultProject), tc.waiting, nil)
			w := &bytes.Buffer{}

			err := doc.WriteTable(w, time.UTC)
			if err != nil {
				t.Fatalf("WriteTable() error = %v", err)
			}

			got := w.String()
			rest, found := strings.CutPrefix(got, tc.wantTable)
			if !found {
				t.Fatalf("got %q, want it to start with %q", got, tc.wantTable)
			}

			if tc.wantWaiting == "" {
				if strings.Contains(rest, "waiting") || strings.Contains(rest, "tt attach") {
					t.Errorf("want no waiting hint, got %q", rest)
				}
			} else {
				if !strings.Contains(rest, tc.wantWaiting) {
					t.Errorf("got %q, want it to contain %q", rest, tc.wantWaiting)
				}
				if !strings.Contains(rest, "tt attach --entry") {
					t.Errorf("got %q, want it to suggest tt attach --entry", rest)
				}
			}

			// Each step must come after the one before it
			for _, step := range doc.RollupSteps {
				i := strings.Index(rest, step)
				if i < 0 {
					t.Fatalf("step %q missing or out of order in %q", step, got)
				}
				rest = rest[i+len(step):]
			}
		})
	}
}
