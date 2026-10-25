package store

import (
	"time"
)

// DB times stored with full precision
const dbTimeLayout = "2006-01-02T15:04:05.000000000Z"

// ToDBString accepts an assumed UTC time and returns a formatted string
// suitable for storing in the SQLite database
func ToDBString(t time.Time) string {
	return t.UTC().Format(dbTimeLayout)
}

// FromNullableDBString converts a nullable time string
// stored in the SQLite database into a UTC time.Time
func FromNullableDBString(t *string) (*time.Time, error) {
	if t == nil {
		return nil, nil
	}

	parsedTime, err := FromDBString(*t)
	if err != nil {
		return nil, err
	}
	return &parsedTime, nil
}

// FromDBString converts a non-nullable time string stored
// in the SQLite database into a UTC time.Time
func FromDBString(t string) (time.Time, error) {
	parsed, err := time.Parse(dbTimeLayout, t)
	return parsed, err
}
