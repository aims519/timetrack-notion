package store

import (
	"testing"
	"time"
)

func TestDBString_RoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		input      time.Time
		wantStored string
	}{
		{"whole_seconds", time.Date(2026, 1, 1, 9, 30, 15, 0, time.UTC), "2026-01-01T09:30:15.000000000Z"},
		{"nanoseconds", time.Date(2026, 1, 1, 9, 30, 15, 123456789, time.UTC), "2026-01-01T09:30:15.123456789Z"},
		{"trailing_zero_nanoseconds", time.Date(2026, 1, 1, 9, 30, 15, 500000000, time.UTC), "2026-01-01T09:30:15.500000000Z"},
		{"plus_one_hour", time.Date(2026, 1, 1, 10, 30, 15, 0, time.FixedZone("plus1", 60*60)), "2026-01-01T09:30:15.000000000Z"},
		{"cross_midnight", time.Date(2025, 12, 31, 23, 0, 0, 0, time.FixedZone("minus5", -5*60*60)), "2026-01-01T04:00:00.000000000Z"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stored := ToDBString(tc.input)
			if stored != tc.wantStored {
				t.Errorf("ToDBString() want %q, got %q", tc.wantStored, stored)
			}

			got, err := FromDBString(stored)
			if err != nil {
				t.Fatalf("FromDBString(%q) want nil error, got %v", stored, err)
			}

			if !got.Equal(tc.input) {
				t.Errorf("round trip want %v, got %v", tc.input, got)
			}

			if got.Location() != time.UTC {
				t.Errorf("FromDBString() want location UTC, got %v", got.Location())
			}
		})
	}
}

func TestNullableDBString_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		null  bool
		input time.Time
	}{
		{"null", true, time.Time{}},
		{"whole_seconds", false, time.Date(2026, 1, 1, 9, 30, 15, 0, time.UTC)},
		{"nanoseconds", false, time.Date(2026, 1, 1, 9, 30, 15, 123456789, time.UTC)},
		{"plus_one_hour", false, time.Date(2026, 1, 1, 10, 30, 15, 0, time.FixedZone("plus1", 60*60))},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stored *string
			if !tc.null {
				s := ToDBString(tc.input)
				stored = &s
			}

			got, err := FromNullableDBString(stored)
			if err != nil {
				t.Fatalf("FromNullableDBString() want nil error, got %v", err)
			}

			if tc.null {
				if got != nil {
					t.Errorf("FromNullableDBString(nil) want nil, got %v", *got)
				}
				return
			}

			if got == nil {
				t.Fatalf("FromNullableDBString(%q) want a time, got nil", *stored)
			}

			if !got.Equal(tc.input) {
				t.Errorf("round trip want %v, got %v", tc.input, *got)
			}
		})
	}
}
