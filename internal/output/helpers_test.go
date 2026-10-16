package output

import (
	"encoding/json"
	"testing"
	"time"
)

func newTestEntry() Entry {
	return Entry{
		ID:        "1234",
		Start:     NewUTCTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		SyncState: "pending",
	}
}

// marshalAndDecodeDoc works for any document type, since json.Marshal takes any
func marshalAndDecodeDoc(t *testing.T, doc any) map[string]any {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("unable to marshal test document: %v", err)
	}

	result := make(map[string]any)
	unmarshalErr := json.Unmarshal(data, &result)
	if unmarshalErr != nil {
		t.Fatalf("unable to unmarshal document JSON: %v", unmarshalErr)
	}

	return result
}
