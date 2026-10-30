package cli

import (
	"time"

	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
	"github.com/aims519/timetrack-notion/internal/timeparse"
)

const staleTimerThreshold = 12 * time.Hour

// mapNullableDBEntry is a wrapper to handle cases where a result from
// the DB may be nil
func mapNullableDBEntry(e *store.Entry) *output.Entry {
	if e == nil {
		return nil
	}

	result := mapDBEntry(*e)
	return &result
}

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

// mapDBStatus maps a status retrieved from the store into an output format
func mapDBStatus(s store.Status, nowUTC time.Time) output.StatusDoc {
	runningEntry := mapNullableDBEntry(s.Running)
	var runningElapsedSeconds int
	if runningEntry != nil {
		runningElapsedSeconds = *elapsedSeconds(s.Running.StartUTC, &nowUTC)
	}

	// Sum total minutes for today's entries. A running entry started today counts up until now.
	totalTodayMinutes := 0
	for _, e := range s.TodayEntries {
		if e.EndUTC != nil {
			totalTodayMinutes += timeparse.EntryMinutes(e.StartUTC, *e.EndUTC)
		} else {
			totalTodayMinutes += timeparse.EntryMinutes(e.StartUTC, nowUTC)
		}
	}

	todayCounts := output.TodayCounts{
		Entries: len(s.TodayEntries),
		Minutes: totalTodayMinutes,
	}

	countsSummary := output.CountsSummary{
		Pending: s.CountSummary.Pending,
		Waiting: s.CountSummary.Waiting,
		Blocked: s.CountSummary.Blocked,
	}

	warnings := runningEntryWarnings(s.Running, nowUTC)

	result := output.NewStatusDoc(runningEntry,
		runningElapsedSeconds,
		todayCounts,
		countsSummary,
		s.Connected,
		warnings,
	)
	return result
}

// runningEntryWarnings returns a warning if the entry has been running
// for > 12h
func runningEntryWarnings(e *store.Entry, nowUTC time.Time) []output.Warning {
	if e == nil {
		return []output.Warning{}
	}

	if duration := nowUTC.Sub(e.StartUTC); duration > staleTimerThreshold {
		return []output.Warning{{Code: output.StaleTimer, Message: "timer has been running for over 12 hours"}}
	}

	return []output.Warning{}
}
