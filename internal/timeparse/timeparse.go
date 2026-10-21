package timeparse

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

const wallClockTimeFormat = "2006-01-02T15:04:05"

// CurrentLocation returns the system time.Location
// unless a valid TT_TIMEZONE environment variable is set
func CurrentLocation() (*time.Location, error) {
	tz, exists := os.LookupEnv("TT_TIMEZONE")
	if exists {
		if tz == "" {
			return nil, &clierr.Error{Code: clierr.Usage, Message: "TT_TIMEZONE set and empty"}
		}

		loc, err := time.LoadLocation(tz)
		if err != nil {
			return nil, &clierr.Error{Code: clierr.Usage, Message: fmt.Sprintf("invalid TT_TIMEZONE %q", tz), Cause: err}
		}

		return loc, nil
	}

	// use system TZ by default
	return time.Local, nil
}

// Parse reads a time argument (--at, --start, --end) as either HH:MM,
// meaning today in loc, or RFC 3339. The result is in UTC
func Parse(input string, now time.Time, loc *time.Location) (time.Time, error) {
	var t time.Time
	var err error

	// RFC 3339 always separates date and time with a T; HH:MM never has one
	if strings.Contains(input, "T") || strings.Contains(input, "t") {
		t, err = parseRFC3339(input, now)
	} else {
		t, err = todayAtHHMM(input, now, loc)
	}
	if err != nil {
		return time.Time{}, err
	}

	return t.UTC(), nil
}

// ParseAgo reads the --ago value and returns a UTC result in the past
func ParseAgo(input string, now time.Time) (time.Time, error) {
	d, err := ParseDuration(input)
	if err != nil {
		return time.Time{}, err
	}

	return now.Add(-1 * d).UTC(), nil
}

// ParseDuration reads a duration passed in via --ago or --duration.
// Duration should not be negative, with zeroes allowed
func ParseDuration(input string) (time.Duration, error) {
	d, err := time.ParseDuration(input)
	if err != nil {
		return 0, &clierr.Error{Code: clierr.InvalidTime, Message: "unable to parse duration, require e.g. 10m or 1h15m", Cause: err, Details: map[string]any{"input": input}}
	}

	if d < 0 {
		return 0, &clierr.Error{Code: clierr.InvalidTime, Message: "negative durations aren't allowed", Details: map[string]any{"input": input}}
	}

	return d, nil
}

// parseRFC3339 reads an RFC 3339 time. Its offset names a single instant,
// so unlike a wall-clock time it can't be missing or repeated
func parseRFC3339(input string, now time.Time) (time.Time, error) {
	instant, err := time.Parse(time.RFC3339, input)
	if err != nil {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: "required format RFC 3339, e.g. 2026-10-24T12:00:00+01:00", Cause: err, Details: map[string]any{"input": input}}
	}

	if instant.After(now) {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: "time is in the future", Details: map[string]any{"input": input}}
	}

	return instant, nil
}

// checkWallClockTime reads input (wallClockTimeFormat, no offset) as a
// time in loc, rejecting it if it's in the future, or doesn't exist or
// repeats on a DST change day
func checkWallClockTime(input string, now time.Time, loc *time.Location) (time.Time, error) {
	layout := wallClockTimeFormat

	pickedInstant, err := time.ParseInLocation(layout, input, loc)
	if err != nil {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: "could not be parsed with location", Cause: err, Details: map[string]any{"input": input}}
	}

	if pickedInstant.After(now) {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: "time is in the future", Details: map[string]any{"input": input}}
	}

	readingAsUTC, err := time.Parse(layout, input)
	if err != nil {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: "could not be parsed", Cause: err, Details: map[string]any{"input": input}}
	}

	// Check time exists (formatted picked instant matches formatted intended reading)
	want := readingAsUTC.Format(layout)
	if got := pickedInstant.Format(layout); got != want {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: "time does not exist, provide an offset", Details: map[string]any{"input": input}}
	}

	if timeRepeats(pickedInstant, readingAsUTC, loc) {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: "time is ambiguous, provide an offset", Details: map[string]any{"input": input}}
	}

	return pickedInstant, nil
}

// timeRepeats checks for ambiguous times, i.e. a time that repeats
// on a given day due to timezone offset changes. readingAsUTC must hold
// the wall-clock reading in UTC, as parsed from wallClockTimeFormat
func timeRepeats(picked time.Time, readingAsUTC time.Time, loc *time.Location) bool {
	layout := wallClockTimeFormat

	// Find time offsets either side of the picked instant
	offsets := make([]int, 2)

	dayBefore := picked.Add(-24 * time.Hour)
	_, dayBeforeOffset := dayBefore.Zone()
	offsets[0] = dayBeforeOffset

	_, pickedOffset := picked.Zone()

	dayAfter := picked.Add(24 * time.Hour)
	_, dayAfterOffset := dayAfter.Zone()
	offsets[1] = dayAfterOffset

	// Test if applying offsets different to the picked instant's
	// results in an identical reading => the reading repeats
	for _, probeOffset := range offsets {
		if probeOffset == pickedOffset {
			continue
		}

		// reading = UTC + offset => UTC = reading - offset
		candidateInstant := readingAsUTC.Add(-time.Duration(probeOffset) * time.Second).In(loc)

		if candidateInstant.Format(layout) == readingAsUTC.Format(layout) {
			return true
		}
	}

	return false
}

// todayAtHHMM returns a time on the current day in loc at input (HH:MM)
func todayAtHHMM(input string, now time.Time, loc *time.Location) (time.Time, error) {
	segments := strings.Split(input, ":")
	if len(segments) != 2 {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: "required format HH:MM", Details: map[string]any{"input": input}}
	}

	hours, err := strconv.Atoi(segments[0])
	if err != nil {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: fmt.Sprintf("%v is not a valid hour", segments[0]), Details: map[string]any{"input": input}}
	}
	if hours < 0 || hours > 23 {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: fmt.Sprintf("%v is not a valid hour", hours), Details: map[string]any{"input": input}}
	}

	minutes, err := strconv.Atoi(segments[1])
	if err != nil {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: fmt.Sprintf("%v is not a valid minute", segments[1]), Details: map[string]any{"input": input}}
	}
	if minutes < 0 || minutes > 59 {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: fmt.Sprintf("%v is not a valid minute", minutes), Details: map[string]any{"input": input}}
	}

	year, month, day := now.In(loc).Date()

	// check result is valid time before returning
	requestedTimeStr := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:00", year, month, day, hours, minutes)
	return checkWallClockTime(requestedTimeStr, now, loc)
}

// ValidateStartAndEnd returns an error if end <= start
func ValidateStartAndEnd(start, end time.Time) error {
	if start.Equal(end) {
		return &clierr.Error{Code: clierr.InvalidTime, Message: "start cannot equal end"}
	}
	if start.After(end) {
		return &clierr.Error{Code: clierr.InvalidTime, Message: "start must be before end"}
	}

	return nil
}

// EntryMinutes returns the minutes between the truncated start and truncated end times
// This number agrees with Notion's truncated output
func EntryMinutes(start, end time.Time) int {
	truncatedStart := start.UTC().Truncate(time.Minute)
	truncatedEnd := end.UTC().Truncate(time.Minute)

	diffDuration := truncatedEnd.Sub(truncatedStart)
	return int(diffDuration.Minutes())
}
