package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestRun_FlagErrors_ExitWithUsageCode(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{"valid_output", []string{"--output", "json"}, 0},
		{"invalid_output", []string{"--output", "xml"}, 2},
		{"unknown_flag", []string{"--bogus"}, 2},
		{"json_unknown_flag", []string{"--output", "json", "--bogus"}, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			got := Run(context.Background(), tc.args, &stdout, &stderr)

			if got != tc.wantCode {
				t.Errorf("Run(%v) = %d, want %d (stderr: %q)", tc.args, got, tc.wantCode, stderr.String())
			}

			if tc.wantCode != 0 && stderr.Len() == 0 {
				t.Errorf("Run(%v) wrote nothing to stderr on failure", tc.args)
			}

			// A failed command must write nothing to stdout
			if tc.wantCode != 0 && stdout.Len() != 0 {
				t.Errorf("Run(%v) wrote to stdout on failure: %q", tc.args, stdout.String())
			}
		})
	}
}
