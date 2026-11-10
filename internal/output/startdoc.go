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
	_, err := fmt.Fprint(w, taskStartedMessage(s.Entry, startFormatted))
	if err != nil {
		return err
	}

	return nil
}

// taskStartedMessage returns a formatted string to describe a started Entry and/or attached Task
func taskStartedMessage(e Entry, startTime string) string {
	var startedMessage string
	if e.Task != nil && e.Description != nil { // task attached with entry description
		startedMessage = fmt.Sprintf("Started tracking \"%s\" on %s at %s (%s)\n", formatEntryLabel(e), formatTaskLabel(*e.Task), startTime, formatEntryShortID(e))
	} else if e.Task != nil { // task but no description
		startedMessage = fmt.Sprintf("Started tracking %s at %s (%s)\n", formatTaskLabel(*e.Task), startTime, formatEntryShortID(e))
	} else { // no task
		startedMessage = fmt.Sprintf("Started tracking \"%s\" at %s (%s)\n", formatEntryLabel(e), startTime, formatEntryShortID(e))
	}

	return startedMessage
}
