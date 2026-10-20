package timeparse

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

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

func ValidTime(input string, layout string, loc *time.Location, now time.Time) (time.Time, error) {
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

	if timeRepeats(pickedInstant, readingAsUTC, layout, loc) {
		return time.Time{}, &clierr.Error{Code: clierr.InvalidTime, Message: "time is ambiguous, provide an offset", Details: map[string]any{"input": input}}
	}

	return pickedInstant, nil
}

// timeRepeats checks for ambiguous times, i.e. a time that repeats
// on a given day due to timezone offset changes
func timeRepeats(picked time.Time, readingAsUTC time.Time, layout string, loc *time.Location) bool {
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

// TodayAtHHMM returns a time on the current day at input (HH:MM)
func TodayAtHHMM(input string, now time.Time, loc *time.Location) (time.Time, error) {
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
	formatWallClock := "2006-01-02T15:04:05" // YYYY-MM-DDTHH:mm:ss
	requestedTimeStr := fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:00", year, month, day, hours, minutes)
	return ValidTime(requestedTimeStr, formatWallClock, loc, now)
}
