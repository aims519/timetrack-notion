package output

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestStopDoc_Marshal_EmbeddedFieldsFlattenedToTopLevel(t *testing.T) {
	e := newTestEntry()
	end := NewUTCTime(time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC))
	e.End = &end

	doc := NewStopDoc(e, nil)

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

	if value, ok := entry["end"]; !ok || value != "2026-01-01T01:00:00Z" {
		t.Errorf("Got entry.end = %v, want %q", value, "2026-01-01T01:00:00Z")
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

	// The same keys as EntryDoc, which stop shares with add, attach and edit (§8)
	if len(resultsMap) != 3 {
		t.Errorf("Got %d keys, want 3: %v", len(resultsMap), resultsMap)
	}
}

func TestStopDoc_WriteTable_WritesExpectedOutput(t *testing.T) {
	const fullID = "0605115f-a394-4b6d-a8f7-528408bbc44f"
	description := "writing tests"
	quoted := `say "hi"`
	start := time.Date(2025, 12, 31, 20, 0, 0, 0, time.UTC)

	// want is the line without its trailing newline, so it can be a raw string
	tests := []struct {
		name        string
		id          string
		description *string
		end         time.Time
		loc         *time.Location
		want        string
	}{
		{"with_description", fullID, &description, time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC), time.UTC,
			`Stopped tracking "writing tests" at 09:30 (0605115)`},
		{"no_description", fullID, nil, time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC), time.UTC,
			`Stopped tracking "(no description)" at 09:30 (0605115)`},
		{"seconds_not_shown", fullID, &description, time.Date(2026, 1, 1, 9, 30, 59, 0, time.UTC), time.UTC,
			`Stopped tracking "writing tests" at 09:30 (0605115)`},
		{"local_zone", fullID, &description, time.Date(2026, 1, 1, 13, 30, 0, 0, time.UTC), time.FixedZone("minus4", -4*60*60),
			`Stopped tracking "writing tests" at 09:30 (0605115)`},
		{"local_zone_previous_day", fullID, &description, time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC), time.FixedZone("minus5", -5*60*60),
			`Stopped tracking "writing tests" at 21:00 (0605115)`},
		{"description_with_quotes", fullID, &quoted, time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC), time.UTC,
			`Stopped tracking "say "hi"" at 09:30 (0605115)`},
		{"id_shorter_than_seven", "1234", &description, time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC), time.UTC,
			`Stopped tracking "writing tests" at 09:30 (1234)`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			end := NewUTCTime(tc.end)
			e := Entry{ID: tc.id, Start: NewUTCTime(start), End: &end, Description: tc.description, SyncState: "pending"}
			doc := NewStopDoc(e, nil)
			w := &bytes.Buffer{}

			err := doc.WriteTable(w, tc.loc)
			if err != nil {
				t.Fatalf("WriteTable() error = %v", err)
			}

			if got, want := w.String(), tc.want+"\n"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestStopDoc_WriteTable_RunningEntry_ReturnsError(t *testing.T) {
	// StopTimer always returns an ended entry, so a nil End is a bug rather than a user error
	doc := NewStopDoc(newTestEntry(), nil)
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
