package cli

import (
	"time"

	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/aims519/timetrack-notion/internal/timeparse"
)

// mapDBEntry maps the properties of an Entry returned from
// the store into an output.Entry
func mapDBEntry(e store.Entry) output.Entry {
	mapped := output.Entry{
		ID:          e.ID,
		Start:       output.NewUTCTime(e.StartUTC),
		Description: e.Description,
		SyncState:   e.SyncState,
	}

	if e.EndUTC != nil {
		t := output.NewUTCTime(*e.EndUTC)
		mapped.End = &t
	}

	mapped.Minutes = elapsedMinutes(e.StartUTC, e.EndUTC)
	mapped.Seconds = elapsedSeconds(e.StartUTC, e.EndUTC)

	return mapped
}

func elapsedMinutes(start time.Time, end *time.Time) *int {
	if end == nil {
		return nil
	}

	result := timeparse.EntryMinutes(start, *end)
	return &result
}

func elapsedSeconds(start time.Time, end *time.Time) *int {
	if end == nil {
		return nil
	}

	diff := end.Sub(start).Round(time.Second).Seconds()
	result := int(diff)
	return &result
}
