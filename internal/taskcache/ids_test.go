package taskcache

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestShortIDs_Unique_ReturnsLast6Characters(t *testing.T) {
	all := []string{"onetwothree", "fourfivesix"}

	got, err := ShortIDs(all)
	if err != nil {
		t.Fatalf("ShortIDs() error = %v", err)
	}

	if got["onetwothree"] != "othree" {
		t.Errorf("got %q:%q, want %q", "onetwothree", got["onetwothree"], "othree")
	}

	if got["fourfivesix"] != "ivesix" {
		t.Errorf("got %q:%q, want %q", "fourfivesix", got["fourfivesix"], "ivesix")
	}
}

func TestShortIDs_Success(t *testing.T) {
	tests := []struct {
		name string
		all  []string
		want map[string]string
	}{
		// No shared suffixes, so every ID gets the minimum 6 characters
		{
			name: "single_id",
			all:  []string{"0a1b2c3d4e5f60718293a4b5c6d7e8f9"},
			want: map[string]string{"0a1b2c3d4e5f60718293a4b5c6d7e8f9": "d7e8f9"},
		},
		{
			// Pages created together share prefixes, not suffixes (DESIGN.md §3.1)
			name: "shared_prefix_only",
			all: []string{
				"2f3a9c10aa11bb22cc33dd44ee55ff66",
				"2f3a9c10aa11bb22cc33dd44ee55ab12",
				"2f3a9c10aa11bb22cc33dd44ee55cd34",
			},
			want: map[string]string{
				"2f3a9c10aa11bb22cc33dd44ee55ff66": "55ff66",
				"2f3a9c10aa11bb22cc33dd44ee55ab12": "55ab12",
				"2f3a9c10aa11bb22cc33dd44ee55cd34": "55cd34",
			},
		},
		{
			name: "ids_exactly_6_characters",
			all:  []string{"abcdef", "ghijkl"},
			want: map[string]string{"abcdef": "abcdef", "ghijkl": "ghijkl"},
		},
		// Shared suffixes, so some short IDs need more than 6 characters
		{
			// Both end in "abcdef", so a 6-character short ID would match both of them.
			// Both need extending, not just the one seen second
			name: "two_share_6_characters",
			all:  []string{"1111111aabcdef", "2222222babcdef"},
			want: map[string]string{"1111111aabcdef": "aabcdef", "2222222babcdef": "babcdef"},
		},
		{
			name: "two_share_8_characters",
			all:  []string{"11111qzzabcdef", "11111rzzabcdef"},
			want: map[string]string{"11111qzzabcdef": "qzzabcdef", "11111rzzabcdef": "rzzabcdef"},
		},
		{
			// Only the pair that collides gets longer
			name: "unrelated_id_keeps_6_characters",
			all:  []string{"1111111aabcdef", "2222222babcdef", "3333333ghijkl"},
			want: map[string]string{
				"1111111aabcdef": "aabcdef",
				"2222222babcdef": "babcdef",
				"3333333ghijkl":  "ghijkl",
			},
		},
		{
			// The first ID shares 8 characters with the third and 6 with the second.
			// Each ID's length comes from the longest suffix it shares with any other ID
			name: "longest_shared_suffix_decides",
			all:  []string{"11111azzabcdef", "333333yabcdef", "22222czzabcdef"},
			want: map[string]string{
				"11111azzabcdef": "azzabcdef",
				"333333yabcdef":  "yabcdef",
				"22222czzabcdef": "czzabcdef",
			},
		},
		{
			// The IDs only differ in their first character, so the whole ID is needed
			name: "unique_only_at_full_length",
			all:  []string{"a111111111", "b111111111"},
			want: map[string]string{"a111111111": "a111111111", "b111111111": "b111111111"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The result must not depend on the order the cache returns the IDs in,
			// so each case runs forwards and reversed
			reversed := slices.Clone(tc.all)
			slices.Reverse(reversed)

			for _, all := range [][]string{tc.all, reversed} {
				got, err := ShortIDs(all)
				if err != nil {
					t.Fatalf("ShortIDs(%q) want nil error, got %v", all, err)
				}

				if !maps.Equal(got, tc.want) {
					t.Errorf("ShortIDs(%q) = %v, want %v", all, got, tc.want)
				}

				checkShortIDsResolve(t, all, got)
			}
		})
	}
}

func TestShortIDs_Errors(t *testing.T) {
	tests := []struct {
		name string
		all  []string
	}{
		// Two or more identical IDs, so no suffix can tell them apart
		{"two_identical", []string{"abcdefgh", "abcdefgh"}},
		{"two_identical_6_characters", []string{"abcdef", "abcdef"}},
		{"identical_among_unique", []string{"abcdefgh", "1234567890", "abcdefgh"}},
		{"three_identical", []string{"abcdefgh", "abcdefgh", "abcdefgh"}},
		// An ID shorter than 6 characters can't have a 6-character short ID
		{"5_characters", []string{"abcde"}},
		{"empty_string", []string{""}},
		{"short_id_after_valid_ids", []string{"abcdefgh", "1234567890", "12345"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShortIDs(tc.all)
			if err == nil {
				t.Fatalf("ShortIDs(%q) want error, got nil and %v", tc.all, got)
			}

			var ce *clierr.Error
			if !errors.As(err, &ce) {
				t.Fatalf("error %v is not a *clierr.Error", err)
			}
			if ce.Code != clierr.Internal {
				t.Errorf("error code = %q, want %q", ce.Code, clierr.Internal)
			}

			if got != nil {
				t.Errorf("ShortIDs(%q) want nil map with an error, got %v", tc.all, got)
			}
		})
	}
}

// checkShortIDsResolve checks every ID has a short ID that is a suffix of it, and that
// the short ID is a suffix of no other ID, so typing it finds exactly one task
func checkShortIDsResolve(t *testing.T, all []string, got map[string]string) {
	t.Helper()

	if len(got) != len(all) {
		t.Errorf("got %d short IDs for %d IDs", len(got), len(all))
	}

	for _, id := range all {
		short, ok := got[id]
		if !ok {
			t.Errorf("no short ID for %q", id)
			continue
		}
		if len(short) < 6 {
			t.Errorf("short ID %q for %q is under 6 characters", short, id)
		}
		if !strings.HasSuffix(id, short) {
			t.Errorf("short ID %q is not a suffix of %q", short, id)
		}

		for _, other := range all {
			if other != id && strings.HasSuffix(other, short) {
				t.Errorf("short ID %q for %q also matches %q", short, id, other)
			}
		}
	}
}
