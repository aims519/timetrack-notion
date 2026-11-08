package output

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestAttachDoc_Marshal_EmbeddedFieldsFlattenedToTopLevel(t *testing.T) {
	e := newTestEntry()
	e.Task = &TaskRef{PageID: "3f63b23e837922", ShortID: "837922", Name: "Write the sync engine"}

	doc := NewAttachDoc(e, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

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

	task, ok := entry["task"].(map[string]any)
	if !ok {
		t.Fatalf("Got entry.task = %v, want an object", entry["task"])
	}
	if value, ok := task["short_id"]; !ok || value != "837922" {
		t.Errorf("Got entry.task.short_id = %v, want %q", value, "837922")
	}

	if _, ok := resultsMap["warnings"].([]any); !ok {
		t.Errorf("Got warnings = %v, want an array", resultsMap["warnings"])
	}

	// A nested EntryDoc/BaseSchema/BaseWarnings key would mean the embedding is not flattened
	for _, key := range []string{"EntryDoc", "BaseSchema", "BaseWarnings"} {
		if _, found := resultsMap[key]; found {
			t.Errorf("%s key should not be present", key)
		}
	}

	// The same keys as EntryDoc, which attach shares with stop, add and edit (§8)
	if len(resultsMap) != 3 {
		t.Errorf("Got %d keys, want 3: %v", len(resultsMap), resultsMap)
	}
}

func TestAttachDoc_WriteTable_WritesExpectedOutput(t *testing.T) {
	const fullID = "0605115f-a394-4b6d-a8f7-528408bbc44f"
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	end := NewUTCTime(time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC))

	// want is the line without its trailing newline, so it can be a raw string
	tests := []struct {
		name        string
		id          string
		description *string
		taskName    string
		running     bool
		want        string
	}{
		{"stopped_entry", fullID, ptr("writing tests"), "Write the sync engine", false,
			`Attached "Write the sync engine" to "writing tests" (0605115)`},
		// Attaching to the running entry reads the same; there's no time to show
		{"running_entry", fullID, ptr("writing tests"), "Write the sync engine", true,
			`Attached "Write the sync engine" to "writing tests" (0605115)`},
		// With no description to quote, the entry is named by its short ID alone
		{"no_description", fullID, nil, "Write the sync engine", false,
			`Attached "Write the sync engine" to 0605115`},
		{"no_description_id_shorter_than_seven", "1234", nil, "Write the sync engine", false,
			`Attached "Write the sync engine" to 1234`},
		{"quotes_in_names", fullID, ptr(`say "hi"`), `Plan "v2"`, false,
			`Attached "Plan "v2"" to "say "hi"" (0605115)`},
		{"id_shorter_than_seven", "1234", ptr("writing tests"), "Write the sync engine", false,
			`Attached "Write the sync engine" to "writing tests" (1234)`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := Entry{
				ID:          tc.id,
				Start:       NewUTCTime(start),
				Description: tc.description,
				Task:        &TaskRef{PageID: "3f63b23e837922", ShortID: "837922", Name: tc.taskName},
				SyncState:   "pending",
			}
			if !tc.running {
				e.End = &end
			}
			doc := NewAttachDoc(e, nil)
			w := &bytes.Buffer{}

			// The location doesn't matter, since no time is shown
			err := doc.WriteTable(w, time.FixedZone("minus4", -4*60*60))
			if err != nil {
				t.Fatalf("WriteTable() error = %v", err)
			}

			if got, want := w.String(), tc.want+"\n"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestAttachDoc_WriteTable_NoTask_ReturnsError(t *testing.T) {
	// A successful attach always returns the entry with its task, so a nil Task is a bug rather
	// than a user error
	doc := NewAttachDoc(newTestEntry(), nil)
	w := &bytes.Buffer{}

	err := doc.WriteTable(w, time.UTC)

	var errcli *clierr.Error
	if !errors.As(err, &errcli) {
		t.Fatalf("want *clierr.Error, got %T: %v", err, err)
	}
	if errcli.Code != clierr.Internal {
		t.Errorf("Code want %q, got %q", clierr.Internal, errcli.Code)
	}
	if w.Len() != 0 {
		t.Errorf("want nothing written, got %q", w.String())
	}
}
