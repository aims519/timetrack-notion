package output

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

type TodayCounts struct {
	Entries int `json:"entries"`
	Minutes int `json:"minutes"`
}

type CountsSummary struct {
	Pending int `json:"pending"`
	Blocked int `json:"blocked"`
	Waiting int `json:"waiting"`
}

// StatusDoc used in status command
type StatusDoc struct {
	BaseSchema
	Running        *Entry        `json:"running"`
	ElapsedSeconds *int          `json:"elapsed_seconds"`
	Today          TodayCounts   `json:"today"`
	Counts         CountsSummary `json:"counts"`
	Connected      bool          `json:"connected"`
	BaseWarnings
}

func NewStatusDoc(running *Entry, elapsedSeconds int, today TodayCounts, counts CountsSummary, connected bool, w []Warning) StatusDoc {
	var computedSeconds *int
	if running != nil {
		computedSeconds = &elapsedSeconds
	}
	return StatusDoc{BaseSchema: BaseSchema{SchemaVersion}, Running: running, ElapsedSeconds: computedSeconds, Today: today, Counts: counts, Connected: connected, Warnings: ensureNotNil(w)}
}

func (s StatusDoc) WriteTable(w io.Writer, loc *time.Location) error {
	tabW := tabwriter.NewWriter(w, 0, 0, 3, ' ', tabwriter.TabIndent)

	// Running entry and elapsed seconds
	if s.Running != nil {
		_, _ = fmt.Fprintf(tabW, "Tracking\t\"%s\" (%s), %v\n", formatEntryLabel(*s.Running), formatEntryShortID(*s.Running), formatMinutes(*s.ElapsedSeconds/60))
	}

	// Today summary
	_, _ = fmt.Fprintf(tabW, "Today\t%s, %v\n", formatPlural(s.Today.Entries, "entry", "entries"), formatMinutes(s.Today.Minutes))

	// Overall counts
	_, _ = fmt.Fprintf(tabW, "Sync\t%d pending · %d blocked · %d waiting\n", s.Counts.Pending, s.Counts.Blocked, s.Counts.Waiting)

	// context connected
	if !s.Connected {
		_, _ = fmt.Fprint(tabW, "\nNot connected to Notion: run 'tt init' to sync\n")
	}

	return tabW.Flush()
}
