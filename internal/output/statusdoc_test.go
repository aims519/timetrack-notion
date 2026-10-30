package output

import (
	"bytes"
	"maps"
	"strings"
	"testing"
	"time"
)

func TestStatusDoc_Marshal_NothingRunning_RunningAndElapsedNull(t *testing.T) {
	// A non-zero elapsed shows the constructor drops it when nothing is running
	doc := NewStatusDoc(nil, 90, TodayCounts{}, CountsSummary{}, false, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	if value, ok := resultsMap["schema_version"]; !ok || value != float64(1) {
		t.Errorf("Got schema_version = %v (present: %v), want %v", value, ok, 1)
	}

	for _, key := range []string{"running", "elapsed_seconds"} {
		if value, ok := resultsMap[key]; !ok || value != nil {
			t.Errorf("Got %s = %v (present: %v), want present with value nil", key, value, ok)
		}
	}

	// false is the zero value, so check it is still written rather than omitted
	if value, ok := resultsMap["connected"]; !ok || value != false {
		t.Errorf("Got connected = %v (present: %v), want false", value, ok)
	}

	if _, ok := resultsMap["warnings"].([]any); !ok {
		t.Errorf("Got warnings = %v, want an array", resultsMap["warnings"])
	}

	if len(resultsMap) != 7 {
		t.Errorf("Got %d keys, want 7: %v", len(resultsMap), resultsMap)
	}
}

func TestStatusDoc_Marshal_Running_EntryAndElapsedSet(t *testing.T) {
	running := newTestEntry()
	doc := NewStatusDoc(&running, 90, TodayCounts{}, CountsSummary{}, true, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	runningMap, ok := resultsMap["running"].(map[string]any)
	if !ok {
		t.Fatalf("Got running = %v, want an object", resultsMap["running"])
	}

	if value, ok := runningMap["id"]; !ok || value != "1234" {
		t.Errorf("Got running.id = %v, want %q", value, "1234")
	}

	if value, ok := resultsMap["elapsed_seconds"]; !ok || value != float64(90) {
		t.Errorf("Got elapsed_seconds = %v (present: %v), want %v", value, ok, 90)
	}

	if value, ok := resultsMap["connected"]; !ok || value != true {
		t.Errorf("Got connected = %v (present: %v), want true", value, ok)
	}

	if len(resultsMap) != 7 {
		t.Errorf("Got %d keys, want 7: %v", len(resultsMap), resultsMap)
	}
}

func TestStatusDoc_Marshal_TodayAndCounts_NestedObjectsWithExactKeys(t *testing.T) {
	// Distinct values catch fields being swapped or written under the wrong key
	today := TodayCounts{Entries: 3, Minutes: 95}
	counts := CountsSummary{Pending: 4, Blocked: 1, Waiting: 2}
	doc := NewStatusDoc(nil, 0, today, counts, false, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	tests := []struct {
		key  string
		want map[string]any
	}{
		{"today", map[string]any{"entries": float64(3), "minutes": float64(95)}},
		{"counts", map[string]any{"pending": float64(4), "blocked": float64(1), "waiting": float64(2)}},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			got, ok := resultsMap[tc.key].(map[string]any)
			if !ok {
				t.Fatalf("Got %s = %v, want an object", tc.key, resultsMap[tc.key])
			}

			// maps.Equal checks the keys and values together, so a missing or extra key fails too
			if !maps.Equal(got, tc.want) {
				t.Errorf("Got %s = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestStatusDoc_WriteTable_WritesExpectedOutput(t *testing.T) {
	const fullID = "0605115f-a394-4b6d-a8f7-528408bbc44f"
	description := "writing tests"

	newRunning := func(description *string) *Entry {
		return &Entry{ID: fullID, Start: NewUTCTime(time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)), Description: description, SyncState: "pending"}
	}

	// want holds the lines without their trailing newlines, so they can be raw strings.
	// The first column is as wide as its longest label plus 3 spaces of padding
	tests := []struct {
		name      string
		running   *Entry
		elapsed   int
		today     TodayCounts
		counts    CountsSummary
		connected bool
		want      []string
	}{
		{"nothing_running", nil, 0, TodayCounts{}, CountsSummary{}, true, []string{
			`Today   0 entries, 0m`,
			`Sync    0 pending · 0 blocked · 0 waiting`,
		}},
		{"nothing_running_with_entries_today", nil, 0, TodayCounts{Entries: 3, Minutes: 135}, CountsSummary{Pending: 1, Blocked: 2, Waiting: 3}, true, []string{
			`Today   3 entries, 2h 15m`,
			`Sync    1 pending · 2 blocked · 3 waiting`,
		}},
		{"running", newRunning(&description), 4805, TodayCounts{Entries: 1, Minutes: 80}, CountsSummary{Waiting: 1}, true, []string{
			`Tracking   "writing tests" (0605115), 1h 20m`,
			`Today      1 entry, 1h 20m`,
			`Sync       0 pending · 0 blocked · 1 waiting`,
		}},
		// Elapsed seconds are shown as whole minutes
		{"running_under_a_minute", newRunning(&description), 59, TodayCounts{Entries: 1}, CountsSummary{Waiting: 1}, true, []string{
			`Tracking   "writing tests" (0605115), 0m`,
			`Today      1 entry, 0m`,
			`Sync       0 pending · 0 blocked · 1 waiting`,
		}},
		{"running_no_description", newRunning(nil), 600, TodayCounts{Entries: 1, Minutes: 10}, CountsSummary{Waiting: 1}, true, []string{
			`Tracking   "(no description)" (0605115), 10m`,
			`Today      1 entry, 10m`,
			`Sync       0 pending · 0 blocked · 1 waiting`,
		}},
		// The hint has no tab, so it doesn't widen the first column
		{"not_connected", nil, 0, TodayCounts{}, CountsSummary{}, false, []string{
			`Today   0 entries, 0m`,
			`Sync    0 pending · 0 blocked · 0 waiting`,
			``,
			`Not connected to Notion: run 'tt init' to sync`,
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewStatusDoc(tc.running, tc.elapsed, tc.today, tc.counts, tc.connected, nil)
			w := &bytes.Buffer{}

			err := doc.WriteTable(w, time.UTC)
			if err != nil {
				t.Fatalf("WriteTable() error = %v", err)
			}

			if got, want := w.String(), strings.Join(tc.want, "\n")+"\n"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestStatusDoc_WriteTable_WarningsNotInTable(t *testing.T) {
	running := newTestEntry()
	warnings := []Warning{{Code: StaleTimer, Message: "timer has been running for over 12 hours"}}
	doc := NewStatusDoc(&running, 13*60*60, TodayCounts{}, CountsSummary{}, true, warnings)
	w := &bytes.Buffer{}

	err := doc.WriteTable(w, time.UTC)
	if err != nil {
		t.Fatalf("WriteTable() error = %v", err)
	}

	// Write prints warnings to stderr, so the table must not repeat them (DESIGN.md §8)
	if strings.Contains(w.String(), "12 hours") {
		t.Errorf("table output want no warning, got %q", w.String())
	}
}
