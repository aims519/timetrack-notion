package output

import "testing"

func TestFormatPlural(t *testing.T) {
	tests := []struct {
		name     string
		n        int
		singular string
		plural   string
		want     string
	}{
		// English uses the plural for zero
		{"zero", 0, "entry", "entries", "0 entries"},
		{"one", 1, "entry", "entries", "1 entry"},
		{"two", 2, "entry", "entries", "2 entries"},
		{"many", 12, "entry", "entries", "12 entries"},
		// Both forms are passed in, so irregular plurals work
		{"irregular", 3, "person", "people", "3 people"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatPlural(tc.n, tc.singular, tc.plural); got != tc.want {
				t.Errorf("formatPlural(%d, %q, %q) = %q, want %q", tc.n, tc.singular, tc.plural, got, tc.want)
			}
		})
	}
}

func TestFormatMinutes(t *testing.T) {
	tests := []struct {
		name    string
		minutes int
		want    string
	}{
		{"zero", 0, "0m"},
		{"one_minute", 1, "1m"},
		{"under_an_hour", 59, "59m"},
		{"one_hour", 60, "1h 0m"},
		{"just_over_an_hour", 61, "1h 1m"},
		{"hours_and_minutes", 80, "1h 20m"},
		{"whole_hours", 120, "2h 0m"},
		// No days: a timer left over a weekend is still shown in hours
		{"over_a_day", 25 * 60, "25h 0m"},
		{"over_a_weekend", 62*60 + 5, "62h 5m"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatMinutes(tc.minutes); got != tc.want {
				t.Errorf("formatMinutes(%d) = %q, want %q", tc.minutes, got, tc.want)
			}
		})
	}
}
