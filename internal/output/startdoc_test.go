package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

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

func TestStartDoc_WriteTable_WritesExpectedOutput(t *testing.T) {
	const fullID = "0605115f-a394-4b6d-a8f7-528408bbc44f"
	description := "writing tests"
	quoted := `say "hi"`

	// want is the line without its trailing newline, so it can be a raw string
	tests := []struct {
		name        string
		id          string
		description *string
		start       time.Time
		loc         *time.Location
		want        string
	}{
		{"with_description", fullID, &description, time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC), time.UTC,
			`Started tracking "writing tests" at 09:30 (0605115)`},
		{"no_description", fullID, nil, time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC), time.UTC,
			`Started tracking "(no description)" at 09:30 (0605115)`},
		{"seconds_not_shown", fullID, &description, time.Date(2026, 1, 1, 9, 30, 59, 0, time.UTC), time.UTC,
			`Started tracking "writing tests" at 09:30 (0605115)`},
		{"local_zone", fullID, &description, time.Date(2026, 1, 1, 13, 30, 0, 0, time.UTC), time.FixedZone("minus4", -4*60*60),
			`Started tracking "writing tests" at 09:30 (0605115)`},
		{"local_zone_previous_day", fullID, &description, time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC), time.FixedZone("minus5", -5*60*60),
			`Started tracking "writing tests" at 21:00 (0605115)`},
		{"description_with_quotes", fullID, &quoted, time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC), time.UTC,
			`Started tracking "say "hi"" at 09:30 (0605115)`},
		{"id_shorter_than_seven", "1234", &description, time.Date(2026, 1, 1, 9, 30, 0, 0, time.UTC), time.UTC,
			`Started tracking "writing tests" at 09:30 (1234)`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := Entry{ID: tc.id, Start: NewUTCTime(tc.start), Description: tc.description, SyncState: "pending"}
			doc := NewStartDoc(e, nil, nil)
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

func TestStartDoc_WriteTable_StoppedEntry(t *testing.T) {
	const startedID = "0605115f-a394-4b6d-a8f7-528408bbc44f"
	const stoppedID = "11111111-1111-1111-1111-111111111111"
	started := "writing tests"
	stopped := "organising calendar"

	// A switch stops the old entry at the instant the new one starts
	switchAt := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	newStopped := func(description *string) *Entry {
		end := NewUTCTime(switchAt)
		return &Entry{ID: stoppedID, Start: NewUTCTime(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)), End: &end, Description: description, SyncState: "pending"}
	}

	// want holds the lines without their trailing newlines, so they can be raw strings
	tests := []struct {
		name    string
		stopped *Entry
		loc     *time.Location
		want    []string
	}{
		{"no_stopped_entry", nil, time.UTC, []string{
			`Started tracking "writing tests" at 10:00 (0605115)`,
		}},
		{"stopped_entry", newStopped(&stopped), time.UTC, []string{
			`Stopped tracking "organising calendar" at 10:00 (1111111)`,
			`Started tracking "writing tests" at 10:00 (0605115)`,
		}},
		{"stopped_entry_no_description", newStopped(nil), time.UTC, []string{
			`Stopped tracking "(no description)" at 10:00 (1111111)`,
			`Started tracking "writing tests" at 10:00 (0605115)`,
		}},
		{"stopped_entry_local_zone", newStopped(&stopped), time.FixedZone("minus4", -4*60*60), []string{
			`Stopped tracking "organising calendar" at 06:00 (1111111)`,
			`Started tracking "writing tests" at 06:00 (0605115)`,
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := Entry{ID: startedID, Start: NewUTCTime(switchAt), Description: &started, SyncState: "pending"}
			doc := NewStartDoc(e, tc.stopped, nil)
			w := &bytes.Buffer{}

			err := doc.WriteTable(w, tc.loc)
			if err != nil {
				t.Fatalf("WriteTable() error = %v", err)
			}

			if got, want := w.String(), strings.Join(tc.want, "\n")+"\n"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}
