package output

import (
	"fmt"
	"io"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// AttachDoc is used by the attach command
type AttachDoc struct {
	EntryDoc
}

func NewAttachDoc(e Entry, w []Warning) AttachDoc {
	return AttachDoc{EntryDoc: NewEntryDoc(e, w)}
}

// WriteTable outputs the label of the task that was attached, and which entry it was attached to.
// The entry is labelled using its description if it exists, otherwise with just the shortID
func (d AttachDoc) WriteTable(w io.Writer, loc *time.Location) error {
	if d.Entry.Task == nil {
		return &clierr.Error{Code: clierr.Internal, Message: "task should not be null after an attach"}
	}

	var taskLabel string
	if d.Entry.Task.ShortID != "" {
		taskLabel = fmt.Sprintf("\"%s\" [task %s]", d.Entry.Task.Name, d.Entry.Task.ShortID)
	} else {
		taskLabel = fmt.Sprintf("\"%s\"", d.Entry.Task.Name)
	}

	var entryLabel string
	if d.Entry.Description != nil {
		entryLabel = fmt.Sprintf("\"%s\" (%s)", formatEntryLabel(d.Entry), formatEntryShortID(d.Entry))
	} else {
		entryLabel = formatEntryShortID(d.Entry)
	}

	_, err := fmt.Fprintf(w, "Attached %s to %s\n", taskLabel, entryLabel)

	return err
}
