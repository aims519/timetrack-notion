package output

import (
	"fmt"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

const localTimeFormat = "15:04"

func formatEntryLabel(e Entry) string {
	var label string
	if e.Description != nil {
		label = *e.Description
	} else {
		label = "(no description)"
	}

	return label
}

func formatEntryShortID(e Entry) string {
	var shortID string
	if len(e.ID) >= 7 {
		shortID = e.ID[:7]
	} else {
		shortID = e.ID
	}

	return shortID
}

func formatEntryEndTime(e Entry, loc *time.Location) (string, error) {
	if e.End == nil {
		return "", &clierr.Error{Code: clierr.Internal, Message: "end time not set"}
	}
	endTimeFormatted := e.End.t.In(loc).Format(localTimeFormat)
	return endTimeFormatted, nil
}

func formatPlural(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}

	return fmt.Sprintf("%d %s", n, plural)
}

func formatMinutes(totalMinutes int) string {
	hours, minutes := totalMinutes/60, totalMinutes%60

	if hours == 0 {
		return fmt.Sprintf("%dm", minutes)
	}

	return fmt.Sprintf("%dh %dm", hours, minutes)
}
