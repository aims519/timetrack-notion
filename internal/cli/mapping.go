package cli

import (
	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
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

	return mapped
}
