package output

import (
	"fmt"
	"io"
	"time"
)

// StartDoc is used by the start command
type StartDoc struct {
	BaseSchema
	Entry   Entry  `json:"entry"`
	Stopped *Entry `json:"stopped"`
	BaseWarnings
}

const localTimeFormat = "15:04"

func NewStartDoc(e Entry, stopped *Entry, w []Warning) StartDoc {
	return StartDoc{BaseSchema: BaseSchema{SchemaVersion}, Entry: e, Stopped: stopped, Warnings: ensureNotNil(w)}
}

func (s StartDoc) WriteTable(w io.Writer, loc *time.Location) error {
	if s.Stopped != nil {
		endFormatted := s.Stopped.End.t.In(loc).Format(localTimeFormat)
		_, err := fmt.Fprintf(w, "Stopped tracking \"%s\" at %s (%s)\n", formatLabel(*s.Stopped), endFormatted, formatShortID(*s.Stopped))
		if err != nil {
			return err
		}
	}

	startFormatted := s.Entry.Start.t.In(loc).Format(localTimeFormat)
	_, err := fmt.Fprintf(w, "Started tracking \"%s\" at %s (%s)\n", formatLabel(s.Entry), startFormatted, formatShortID(s.Entry))
	if err != nil {
		return err
	}

	return nil
}

func formatLabel(e Entry) string {
	var label string
	if e.Description != nil {
		label = *e.Description
	} else {
		label = "(no description)"
	}

	return label
}

func formatShortID(e Entry) string {
	var shortID string
	if len(e.ID) >= 7 {
		shortID = e.ID[:7]
	} else {
		shortID = e.ID
	}

	return shortID
}
