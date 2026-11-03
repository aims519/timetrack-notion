package output

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// refreshAt is a non-UTC time with nanoseconds, so the tests also check refreshed_at is
// written in UTC with whole seconds, as stored in tasks_refreshed_at
var refreshAt = time.Date(2026, 11, 3, 10, 15, 0, 123456789, time.FixedZone("plus1", 60*60))

func TestRefreshDoc_Marshal_Incremental_GoneNull(t *testing.T) {
	doc := NewRefreshDoc(false, refreshAt, 412*time.Millisecond, 12, nil, nil)

	resultsMap := marshalAndDecodeDoc(t, doc)

	tests := []struct {
		key  string
		want any
	}{
		{"schema_version", float64(1)},
		// false is the zero value, so check it is still written rather than omitted
		{"full", false},
		{"refreshed_at", "2026-11-03T09:15:00Z"},
		{"fetched", float64(12)},
		// An incremental refresh can't see deleted tasks, so gone is null rather than 0
		{"gone", nil},
		{"duration_ms", float64(412)},
	}

	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			if value, ok := resultsMap[tc.key]; !ok || value != tc.want {
				t.Errorf("Got %s = %v (present: %v), want %v", tc.key, value, ok, tc.want)
			}
		})
	}

	if warnings, ok := resultsMap["warnings"].([]any); !ok || len(warnings) != 0 {
		t.Errorf("Got warnings = %v, want an empty array", resultsMap["warnings"])
	}

	if len(resultsMap) != 7 {
		t.Errorf("Got %d keys, want 7: %v", len(resultsMap), resultsMap)
	}
}

func TestRefreshDoc_Marshal_Full_GoneCounted(t *testing.T) {
	tests := []struct {
		name string
		gone int
	}{
		{"some_gone", 2},
		// 0 is the zero value, so check a full refresh that found nothing gone still writes 0, not null
		{"none_gone", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gone := tc.gone
			doc := NewRefreshDoc(true, refreshAt, 1200*time.Millisecond, 40, &gone, nil)

			resultsMap := marshalAndDecodeDoc(t, doc)

			if value, ok := resultsMap["full"]; !ok || value != true {
				t.Errorf("Got full = %v (present: %v), want true", value, ok)
			}
			if value, ok := resultsMap["gone"]; !ok || value != float64(tc.gone) {
				t.Errorf("Got gone = %v (present: %v), want %d", value, ok, tc.gone)
			}
			if value, ok := resultsMap["fetched"]; !ok || value != float64(40) {
				t.Errorf("Got fetched = %v (present: %v), want 40", value, ok)
			}
			if len(resultsMap) != 7 {
				t.Errorf("Got %d keys, want 7: %v", len(resultsMap), resultsMap)
			}
		})
	}
}

func TestRefreshDoc_Marshal_DurationInWholeMilliseconds(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		want     float64
	}{
		{"under_a_millisecond", 900 * time.Microsecond, 0},
		// Milliseconds() truncates, so a part millisecond is dropped rather than rounded
		{"part_millisecond_dropped", 412*time.Millisecond + 900*time.Microsecond, 412},
		{"over_a_second", 1234 * time.Millisecond, 1234},
		{"over_a_minute", 61 * time.Second, 61000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewRefreshDoc(false, refreshAt, tc.duration, 0, nil, nil)

			resultsMap := marshalAndDecodeDoc(t, doc)

			if value, ok := resultsMap["duration_ms"]; !ok || value != tc.want {
				t.Errorf("Got duration_ms = %v (present: %v), want %v", value, ok, tc.want)
			}
		})
	}
}

func TestRefreshDoc_WriteTable_WritesExpectedOutput(t *testing.T) {
	tests := []struct {
		name     string
		full     bool
		duration time.Duration
		fetched  int
		gone     *int
		want     string
	}{
		{"incremental", false, 412 * time.Millisecond, 12, nil, "Refreshed 12 tasks in 412ms"},
		{"one_task", false, 412 * time.Millisecond, 1, nil, "Refreshed 1 task in 412ms"},
		{"no_tasks", false, 80 * time.Millisecond, 0, nil, "Refreshed 0 tasks in 80ms"},
		{"over_a_second", false, 1234 * time.Millisecond, 12, nil, "Refreshed 12 tasks in 1.2s"},
		{"full", true, 1200 * time.Millisecond, 40, ptr(2), "Refreshed 40 tasks in 1.2s, 2 gone"},
		{"full_none_gone", true, 1200 * time.Millisecond, 40, ptr(0), "Refreshed 40 tasks in 1.2s, 0 gone"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewRefreshDoc(tc.full, refreshAt, tc.duration, tc.fetched, tc.gone, nil)
			w := &bytes.Buffer{}

			err := doc.WriteTable(w, time.UTC)
			if err != nil {
				t.Fatalf("WriteTable() error = %v", err)
			}

			if got, want := w.String(), tc.want+"\n"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestRefreshDoc_WriteTable_WarningsNotInTable(t *testing.T) {
	warnings := []Warning{{Code: "test_warning", Message: "something to warn about"}}
	doc := NewRefreshDoc(false, refreshAt, 412*time.Millisecond, 12, nil, warnings)
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

func ptr[T any](v T) *T {
	return &v
}
