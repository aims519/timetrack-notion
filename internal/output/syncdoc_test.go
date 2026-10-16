package output

import (
	"maps"
	"slices"
	"testing"
)

func TestSyncDoc_Marshal_ResultsAlwaysAnArray(t *testing.T) {
	tests := []struct {
		name    string
		results []SyncResult
	}{
		{name: "nil_results", results: nil},
		{name: "empty_results", results: []SyncResult{}},
		{name: "nonempty_results", results: []SyncResult{NewSyncedResult("1")}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewSyncDoc(tc.results, 0, 0, []Warning{})
			resultsMap := marshalAndDecodeDoc(t, doc)

			results, ok := resultsMap["results"].([]any)
			if !ok {
				t.Fatalf("Got results = %v, want an array", resultsMap["results"])
			}

			if len(results) != len(tc.results) {
				t.Errorf("Got %d results, want %d: %v", len(results), len(tc.results), results)
			}
		})
	}
}

func TestSyncDoc_Marshal_SummaryCountsCalculatedCorrectly(t *testing.T) {
	syncedResults := []SyncResult{NewSyncedResult("1"), NewSyncedResult("2"), NewSyncedResult("3")}
	failedResults := []SyncResult{NewFailedResult("4", SyncErrorCodeRateLimited, "rate limited"), NewFailedResult("5", SyncErrorCodeServerError, "server error happened")}
	blockedResults := []SyncResult{NewBlockedResult("6", SyncErrorCodeTaskNotFound, "task not found")}

	allResults := slices.Concat(syncedResults, failedResults, blockedResults)

	waitingCount := 8
	stillBlockedCount := 11

	doc := NewSyncDoc(allResults, waitingCount, stillBlockedCount, []Warning{})

	resultsMap := marshalAndDecodeDoc(t, doc)

	summary, ok := resultsMap["summary"].(map[string]any)
	if !ok {
		t.Fatalf("Got summary = %v, want an object", resultsMap["summary"])
	}

	if value, ok := summary["synced"]; !ok || value != float64(3) {
		t.Errorf("SyncDoc.Summary.Synced = %v, want %v", value, 3)
	}

	if value, ok := summary["failed"]; !ok || value != float64(2) {
		t.Errorf("SyncDoc.Summary.Failed = %v, want %v", value, 2)
	}

	if value, ok := summary["blocked"]; !ok || value != float64(1) {
		t.Errorf("SyncDoc.Summary.Blocked = %v, want %v", value, 1)
	}

	if value, ok := summary["waiting"]; !ok || value != float64(8) {
		t.Errorf("SyncDoc.Summary.Waiting = %v, want %v", value, 8)
	}

	if value, ok := summary["still_blocked"]; !ok || value != float64(11) {
		t.Errorf("SyncDoc.Summary.StillBlocked = %v, want %v", value, 11)
	}

	// count keys to ensure all counts are tested
	if len(summary) != 5 {
		t.Errorf("got %d summary keys, want 5", len(summary))
	}
}

func TestSyncDoc_Marshal_ErrorsCorrectlyNested(t *testing.T) {
	tests := []struct {
		name      string
		result    SyncResult
		wantState string
		wantError map[string]any
	}{
		{"synced", NewSyncedResult("1"), "synced", nil},
		{"failed", NewFailedResult("2", SyncErrorCodeRateLimited, "rate limited"), "failed", map[string]any{"code": "rate_limited", "message": "rate limited"}},
		{"blocked", NewBlockedResult("3", SyncErrorCodeTaskNotFound, "task not found"), "blocked", map[string]any{"code": "task_not_found", "message": "task not found"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewSyncDoc([]SyncResult{tc.result}, 0, 0, []Warning{})
			mapToTest := marshalAndDecodeDoc(t, doc)

			syncResults, ok := mapToTest["results"].([]any)
			if !ok {
				t.Fatalf("got syncResults = %v, want an array", mapToTest["results"])
			}

			if len(syncResults) != 1 {
				t.Fatalf("expected len(syncResults) == 1, got %v", len(syncResults))
			}

			result, ok := syncResults[0].(map[string]any)
			if !ok {
				t.Fatalf("got result = %v, want an object", syncResults[0])
			}

			// Check ID
			if id, ok := result["entry_id"]; !ok || id != tc.result.EntryID {
				t.Errorf("got result.entry_id = %v, want %v", id, tc.result.EntryID)
			}

			// Check state
			if state, ok := result["state"]; !ok || state != tc.wantState {
				t.Errorf("got state = %v, want %v", state, tc.wantState)
			}

			// Check SyncResult key count
			if len(result) != 3 {
				t.Errorf("got len(result) = %v, want 3", len(result))
			}

			// A synced result has a null error, so stop before asserting it as an object
			if tc.wantError == nil {
				if value, ok := result["error"]; !ok || value != nil {
					t.Errorf("got error = %v, want nil", result["error"])
				}
				return
			}

			// Check non-nil errors have all matching properties
			if value, ok := result["error"].(map[string]any); !ok || !maps.Equal(value, tc.wantError) {
				t.Errorf("got error = %v, want %v", result["error"], tc.wantError)
			}
		})
	}
}
