package taskcache

import (
	"fmt"
	"strings"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// ShortIDs returns a map of IDs to shortIDs (min 6 characters), given the full set of IDs as input.
// Each short ID is the shortest suffix that no other ID ends with.
func ShortIDs(all []string) (map[string]string, error) {
	results := make(map[string]string, len(all))
	for i, id := range all {
		suffixLen := 6
		if len(id) < suffixLen {
			return nil, &clierr.Error{Code: clierr.Internal, Message: "at least one ID is too short, must be at least 6 characters", Details: map[string]any{"id": id}}
		}

		suff, err := suffix(id, suffixLen)
		if err != nil {
			return nil, err
		}

		for j, other := range all {
			if i == j {
				continue
			}
			if id == other {
				return nil, &clierr.Error{Code: clierr.Internal, Message: "at least two IDs are identical", Details: map[string]any{"id": id}}
			}

			for strings.HasSuffix(other, suff) {
				suffixLen++
				suff, err = suffix(id, suffixLen)
				if err != nil {
					return nil, err
				}
			}
		}

		results[id] = suff
	}

	return results, nil
}

// suffix returns the last l characters of a string
func suffix(s string, l int) (string, error) {
	if l > len(s) {
		return "", &clierr.Error{Code: clierr.Internal, Message: fmt.Sprintf("invalid suffix length provided, %d, for string length %d", l, len(s))}
	}

	res := s[len(s)-l:]
	return res, nil
}
