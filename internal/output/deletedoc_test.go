package output

import "testing"

// The value of removed is covered separately; this checks the other keys
func TestDeleteDoc_Marshal_AllKeysPresentWithValues(t *testing.T) {
	tests := []struct {
		name   string
		dryRun bool
		action DeleteAction
	}{
		{"dry_run_tombstone", true, DeleteActionTombstone},
		{"real_remove", false, DeleteActionRemove},
		{"real_tombstone", false, DeleteActionTombstone},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc := NewDeleteDoc(tc.dryRun, tc.action, newTestEntry(), nil)

			resultsMap := marshalAndDecodeDoc(t, doc)

			if value, ok := resultsMap["schema_version"]; !ok || value != float64(1) {
				t.Errorf("Got schema_version = %v (present: %v), want %v", value, ok, 1)
			}

			if value, ok := resultsMap["dry_run"]; !ok || value != tc.dryRun {
				t.Errorf("Got dry_run = %v (present: %v), want %v", value, ok, tc.dryRun)
			}

			// The decoded value is a plain string, so convert before comparing
			if value, ok := resultsMap["action"]; !ok || value != string(tc.action) {
				t.Errorf("Got action = %v (present: %v), want %q", value, ok, tc.action)
			}

			if _, ok := resultsMap["entry"].(map[string]any); !ok {
				t.Errorf("Got entry = %v, want an object", resultsMap["entry"])
			}

			if _, ok := resultsMap["warnings"].([]any); !ok {
				t.Errorf("Got warnings = %v, want an array", resultsMap["warnings"])
			}

			if len(resultsMap) != 6 {
				t.Errorf("Got %d keys, want 6: %v", len(resultsMap), resultsMap)
			}
		})
	}
}

func TestNewDeleteDoc_Marshal_RemovedFlagCorrectlySet(t *testing.T) {
	tests := []struct {
		name   string
		dryRun bool
		action DeleteAction
		want   any
	}{
		{"dryrun_remove", true, DeleteActionRemove, nil},
		{"dryrun_tombstone", true, DeleteActionTombstone, nil},
		{"notdryrun_remove", false, DeleteActionRemove, true},
		{"notdryrun_tombstone", false, DeleteActionTombstone, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deleteDoc := NewDeleteDoc(tc.dryRun, tc.action, newTestEntry(), []Warning{})
			resultsMap := marshalAndDecodeDoc(t, deleteDoc)

			if value, ok := resultsMap["removed"]; !ok || value != tc.want {
				t.Errorf("Got DeleteDoc.Removed = %v, want %v", value, tc.want)
			}
		})
	}
}
