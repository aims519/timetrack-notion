package output

import (
	"fmt"
	"io"
	"time"
)

// StopDoc is used by the stop command
type StopDoc struct {
	EntryDoc
}

func NewStopDoc(e Entry, w []Warning) StopDoc {
	return StopDoc{EntryDoc: NewEntryDoc(e, w)}
}

func (s StopDoc) WriteTable(w io.Writer, loc *time.Location) error {
	endFormatted, err := formatEntryEndTime(s.Entry, loc)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Stopped tracking \"%s\" at %s (%s)\n", formatEntryLabel(s.Entry), endFormatted, formatEntryShortID(s.Entry))

	return err
}
