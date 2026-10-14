package output

import (
	"encoding/json"
	"testing"
	"time"
)

func marshalAndDecodeUTCTime(t *testing.T, utc UTCTime) string {
	t.Helper()
	data, err := json.Marshal(utc)
	if err != nil {
		t.Fatalf("unable to marshal test UTCTime: %v", err)
	}

	var result string
	unmarshalErr := json.Unmarshal(data, &result)
	if unmarshalErr != nil {
		t.Fatalf("unable to unmarshal UTCTime JSON: %v", unmarshalErr)
	}

	return result
}

func TestUTCTime_Marshal_ValuesCorrectlyPreserved(t *testing.T) {
	tests := []struct {
		name  string
		input UTCTime
		want  string
	}{
		{"already_utc", NewUTCTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), "2026-01-01T00:00:00Z"},
		{"nanoseconds_truncated", NewUTCTime(time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC)), "2026-01-01T00:00:00Z"},
		{"plus_one_hour", NewUTCTime(time.Date(2026, 1, 1, 1, 0, 0, 0, time.FixedZone("plus1", 60*60))), "2026-01-01T00:00:00Z"},
		{"cross_midnight", NewUTCTime(time.Date(2025, 12, 31, 23, 0, 0, 0, time.FixedZone("minus5", -5*60*60))), "2026-01-01T04:00:00Z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalAndDecodeUTCTime(t, tc.input)
			if got != tc.want {
				t.Errorf("NewUTCTime() want %q, got %q", tc.want, got)
			}
		})
	}
}
