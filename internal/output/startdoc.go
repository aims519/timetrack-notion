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

func NewStartDoc(e Entry, stopped *Entry, w []Warning) StartDoc {
	return StartDoc{BaseSchema: BaseSchema{SchemaVersion}, Entry: e, Stopped: stopped, Warnings: ensureNotNil(w)}
}

func (s StartDoc) WriteTable(w io.Writer, loc *time.Location) error {
	if s.Stopped != nil {
		endFormatted, err := formatEntryEndTime(*s.Stopped, loc)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintf(w, "Stopped tracking \"%s\" at %s (%s)\n", formatEntryLabel(*s.Stopped), endFormatted, formatEntryShortID(*s.Stopped))
		if err != nil {
			return err
		}
	}

	startFormatted := s.Entry.Start.t.In(loc).Format(localTimeFormat)
	_, err := fmt.Fprintf(w, "Started tracking \"%s\" at %s (%s)\n", formatEntryLabel(s.Entry), startFormatted, formatEntryShortID(s.Entry))
	if err != nil {
		return err
	}

	return nil
}
