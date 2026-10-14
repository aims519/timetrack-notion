package output

import (
	"encoding/json"
	"testing"
)

func marshalAndDecodeSync(t *testing.T, s Sync) map[string]any {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("unable to marshal test Sync: %v", err)
	}

	result := make(map[string]any)
	unmarshalErr := json.Unmarshal(data, &result)
	if unmarshalErr != nil {
		t.Fatalf("unable to unmarshal Sync JSON: %v", unmarshalErr)
	}

	return result
}

func TestSync_Marshal_AllKeysPresentWithValues(t *testing.T) {
	tests := []struct {
		name  string
		input Sync
	}{
		// Zero values must still be written: no omitempty on pending or attempted
		{"synced_nothing_pending", Sync{Attempted: true, Result: SyncResultSynced, Pending: 0}},
		{"offline_with_pending", Sync{Attempted: true, Result: SyncResultPending, Pending: 3}},
		{"not_attempted_waiting", Sync{Attempted: false, Result: SyncResultWaiting, Pending: 1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resultsMap := marshalAndDecodeSync(t, tc.input)

			if value, ok := resultsMap["attempted"]; !ok || value != tc.input.Attempted {
				t.Errorf("Got attempted = %v (present: %v), want %v", value, ok, tc.input.Attempted)
			}

			// The decoded value is a plain string, so convert before comparing
			if value, ok := resultsMap["result"]; !ok || value != string(tc.input.Result) {
				t.Errorf("Got result = %v (present: %v), want %q", value, ok, tc.input.Result)
			}

			// JSON numbers decode into map[string]any as float64
			if value, ok := resultsMap["pending"]; !ok || value != float64(tc.input.Pending) {
				t.Errorf("Got pending = %v (present: %v), want %v", value, ok, tc.input.Pending)
			}

			if len(resultsMap) != 3 {
				t.Errorf("Got %d keys, want 3: %v", len(resultsMap), resultsMap)
			}
		})
	}
}
