package output

import (
	"testing"
)

func TestFormat_Set_AcceptsOnlyTableOrJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantValue Format
		wantErr   bool
	}{
		{"table_correct", "table", Table, false},
		{"json_correct", "json", JSON, false},
		{"table_uppercase_incorrect", "TABLE", Table, true},
		{"json_uppercase_incorrect", "JSON", Table, true},
		{"xml_incorrect", "xml", Table, true},
		{"empty_incorrect", "", Table, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := Table
			err := f.Set(tc.input)

			// Errors returned as expected
			if (err != nil) != tc.wantErr {
				t.Fatalf("Set(%q) error = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}

			// Value unchanged if input is rejected
			if tc.wantErr && f != Table {
				t.Errorf("got %q, want %q", f, Table)
			}

			// Set value is correct
			if f != tc.wantValue {
				t.Errorf("got f = %q, want %q", f, tc.wantValue)
			}
		})
	}
}
