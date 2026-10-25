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
	startFormatted := s.Entry.Start.t.In(loc).Format(localTimeFormat)

	var shortID string
	if len(s.Entry.ID) >= 7 {
		shortID = s.Entry.ID[:7]
	} else {
		shortID = s.Entry.ID
	}

	var label string
	if s.Entry.Description != nil {
		label = *s.Entry.Description
	} else {
		label = "(no description)"
	}

	_, err := fmt.Fprintf(w, "Started tracking \"%s\" at %s (%s)\n", label, startFormatted, shortID)
	return err
}
