package store

import "testing"

func TestBuildDSN(t *testing.T) {
	tests := []struct {
		name   string
		dbPath string
		want   string
	}{
		{"valid_dbPath", "/path/to/test.db", "/path/to/test.db?_pragma=foreign_keys%281%29&_pragma=busy_timeout%285000%29&_pragma=journal_mode%28WAL%29&_txlock=immediate"},
		{"dbPath_with_spaces", "/ path/to /test.db", "/ path/to /test.db?_pragma=foreign_keys%281%29&_pragma=busy_timeout%285000%29&_pragma=journal_mode%28WAL%29&_txlock=immediate"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildDSN(tc.dbPath)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
