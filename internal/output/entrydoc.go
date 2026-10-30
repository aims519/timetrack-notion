package output

import (
	"io"
	"time"
)

// EntryDoc is used in the stop, add, attach, edit commands
type EntryDoc struct {
	BaseSchema
	Entry Entry `json:"entry"`
	BaseWarnings
}

func NewEntryDoc(e Entry, w []Warning) EntryDoc {
	return EntryDoc{BaseSchema: BaseSchema{SchemaVersion}, Entry: e, Warnings: ensureNotNil(w)}
}

func (e EntryDoc) WriteTable(w io.Writer, loc *time.Location) error {
	_, err := w.Write([]byte(e.Entry.ID))

	return err
}
