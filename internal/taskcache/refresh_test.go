package taskcache

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/notion"
)

const testContextID = "00000000-0000-0000-0000-000000000000"

func TestMapPageToTask(t *testing.T) {
	const (
		pageID      = "11111111-2222-3333-4444-555555555555"
		wantPageID  = "11111111222233334444555555555555"
		projectA    = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
		wantProjA   = "aaaaaaaabbbbccccddddeeeeeeeeeeee"
		projectB    = "99999999-8888-7777-6666-555555555555"
		titleOneRun = `"Task": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Write tests"}]}`
		oneProject  = `"Project": {"id": "p1", "type": "relation", "relation": [{"id": "` + projectA + `"}], "has_more": false}`
	)

	tests := []struct {
		name        string
		properties  string // the JSON inside "properties": {...}
		wantName    string
		wantProject *string // nil means no project
	}{
		{
			name:        "title and one project",
			properties:  titleOneRun + "," + oneProject,
			wantName:    "Write tests",
			wantProject: ptr(wantProjA),
		},
		{
			name: "title in several pieces is joined",
			properties: `"Task": {"id": "title", "type": "title", "title": [
				{"type": "text", "plain_text": "Write "},
				{"type": "text", "plain_text": "tests", "annotations": {"bold": true}},
				{"type": "text", "plain_text": " for refresh"}
			]},` + oneProject,
			wantName:    "Write tests for refresh",
			wantProject: ptr(wantProjA),
		},
		{
			name:        "no project",
			properties:  titleOneRun + `,"Project": {"id": "p1", "type": "relation", "relation": [], "has_more": false}`,
			wantName:    "Write tests",
			wantProject: nil,
		},
		{
			name:        "several projects takes the first",
			properties:  titleOneRun + `,"Project": {"id": "p1", "type": "relation", "relation": [{"id": "` + projectA + `"}, {"id": "` + projectB + `"}], "has_more": false}`,
			wantName:    "Write tests",
			wantProject: ptr(wantProjA),
		},
		// Edge cases
		{
			name:        "untitled task",
			properties:  `"Task": {"id": "title", "type": "title", "title": []},` + oneProject,
			wantName:    "",
			wantProject: ptr(wantProjA),
		},
		{
			name:        "title property missing",
			properties:  oneProject,
			wantName:    "",
			wantProject: ptr(wantProjA),
		},
		{
			name:        "title property has the wrong type",
			properties:  `"Task": {"id": "t1", "type": "rich_text", "rich_text": [{"type": "text", "plain_text": "Not a title"}]},` + oneProject,
			wantName:    "",
			wantProject: ptr(wantProjA),
		},
		{
			name:        "project property missing",
			properties:  titleOneRun,
			wantName:    "Write tests",
			wantProject: nil,
		},
		{
			name:        "project property has the wrong type",
			properties:  titleOneRun + `,"Project": {"id": "p1", "type": "select", "select": {"name": "Tracker"}}`,
			wantName:    "Write tests",
			wantProject: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := decodePage(t, `{
				"object": "page",
				"id": "`+pageID+`",
				"created_time": "2026-11-02T09:00:00.000Z",
				"last_edited_time": "2026-11-02T09:00:00.000Z",
				"in_trash": false,
				"properties": {`+tc.properties+`}
			}`)

			got := mapPageToTask(p, testContextID)
			if got == nil {
				t.Fatal("mapPageToTask() returned nil")
			}

			if got.ContextID != testContextID {
				t.Errorf("ContextID = %q, want %q", got.ContextID, testContextID)
			}
			if got.PageID != wantPageID {
				t.Errorf("PageID = %q, want %q (no dashes)", got.PageID, wantPageID)
			}
			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}

			switch {
			case tc.wantProject == nil && got.ProjectPageID != nil:
				t.Errorf("ProjectPageID = %q, want nil", *got.ProjectPageID)
			case tc.wantProject != nil && got.ProjectPageID == nil:
				t.Errorf("ProjectPageID = nil, want %q", *tc.wantProject)
			case tc.wantProject != nil && *got.ProjectPageID != *tc.wantProject:
				t.Errorf("ProjectPageID = %q, want %q", *got.ProjectPageID, *tc.wantProject)
			}
		})
	}
}

func TestMapPageToTask_OrderAndDone(t *testing.T) {
	const title = `"Task": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Write tests"}]}`

	// order and done build the "Order" number and "Done" checkbox properties from a JSON value
	order := func(v string) string { return `"Order": {"id": "o1", "type": "number", "number": ` + v + `}` }
	done := func(v string) string { return `"Done": {"id": "d1", "type": "checkbox", "checkbox": ` + v + `}` }

	tests := []struct {
		name       string
		properties string // the JSON inside "properties": {...}
		wantOrder  *float64
		wantDone   bool
	}{
		{
			name:       "order and done set",
			properties: title + "," + order("4.5") + "," + done("true"),
			wantOrder:  ptr(4.5),
			wantDone:   true,
		},
		{
			name:       "not done",
			properties: title + "," + order("4.5") + "," + done("false"),
			wantOrder:  ptr(4.5),
			wantDone:   false,
		},
		{
			name:       "order with no value",
			properties: title + "," + order("null") + "," + done("false"),
			wantOrder:  nil,
		},
		// Edge cases
		{
			name:       "order of zero isn't the same as no order",
			properties: title + "," + order("0") + "," + done("false"),
			wantOrder:  ptr(0.0),
		},
		{
			name:       "decimal order keeps its value",
			properties: title + "," + order("4.1") + "," + done("false"),
			wantOrder:  ptr(4.1),
		},
		{
			name:       "negative order",
			properties: title + "," + order("-1") + "," + done("false"),
			wantOrder:  ptr(-1.0),
		},
		{
			name:       "order property missing",
			properties: title + "," + done("true"),
			wantOrder:  nil,
			wantDone:   true,
		},
		{
			name:       "order property has the wrong type",
			properties: title + `,"Order": {"id": "o1", "type": "rich_text", "rich_text": [{"type": "text", "plain_text": "4.5"}]},` + done("true"),
			wantOrder:  nil,
			wantDone:   true,
		},
		{
			name:       "done property missing",
			properties: title + "," + order("4.5"),
			wantOrder:  ptr(4.5),
			wantDone:   false,
		},
		{
			name:       "done property has the wrong type",
			properties: title + "," + order("4.5") + `,"Done": {"id": "d1", "type": "status", "status": {"name": "Done"}}`,
			wantOrder:  ptr(4.5),
			wantDone:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := decodePage(t, `{
				"object": "page",
				"id": "11111111-2222-3333-4444-555555555555",
				"created_time": "2026-11-02T09:00:00.000Z",
				"last_edited_time": "2026-11-02T09:00:00.000Z",
				"in_trash": false,
				"properties": {`+tc.properties+`}
			}`)

			got := mapPageToTask(p, testContextID)
			if got == nil {
				t.Fatal("mapPageToTask() returned nil")
			}

			switch {
			case tc.wantOrder == nil && got.SortOrder != nil:
				t.Errorf("SortOrder = %v, want nil", *got.SortOrder)
			case tc.wantOrder != nil && got.SortOrder == nil:
				t.Errorf("SortOrder = nil, want %v", *tc.wantOrder)
			case tc.wantOrder != nil && *got.SortOrder != *tc.wantOrder:
				t.Errorf("SortOrder = %v, want %v", *got.SortOrder, *tc.wantOrder)
			}

			if got.Done != tc.wantDone {
				t.Errorf("Done = %v, want %v", got.Done, tc.wantDone)
			}
		})
	}
}

func TestMapPageToTask_Times(t *testing.T) {
	// created_time and last_edited_time differ, so a mix-up between them is caught
	p := decodePage(t, `{
		"object": "page",
		"id": "11111111-2222-3333-4444-555555555555",
		"created_time": "2026-10-20T08:00:00.000Z",
		"last_edited_time": "2026-11-02T09:15:00.000Z",
		"in_trash": false,
		"properties": {"Task": {"id": "title", "type": "title", "title": [{"type": "text", "plain_text": "Write tests"}]}}
	}`)

	got := mapPageToTask(p, testContextID)
	if got == nil {
		t.Fatal("mapPageToTask() returned nil")
	}

	wantEdited := time.Date(2026, 11, 2, 9, 15, 0, 0, time.UTC)
	if !got.LastEditedTime.Equal(wantEdited) {
		t.Errorf("LastEditedTime = %v, want %v", got.LastEditedTime, wantEdited)
	}
	if got.LastEditedTime.Location() != time.UTC {
		t.Errorf("LastEditedTime location = %v, want UTC", got.LastEditedTime.Location())
	}

	// A page Notion returned is present, so it's never gone. Refresh sets gone_at, not the mapping
	if got.GoneAt != nil {
		t.Errorf("GoneAt = %v, want nil", *got.GoneAt)
	}
}

// decodePage decodes a page as Notion returns it, so the test also checks that
// notion.Page's JSON tags match the shapes of the properties
func decodePage(t *testing.T, s string) notion.Page {
	t.Helper()

	var p notion.Page
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		t.Fatalf("decoding page JSON: %v", err)
	}

	return p
}

func ptr[T any](v T) *T {
	return &v
}
