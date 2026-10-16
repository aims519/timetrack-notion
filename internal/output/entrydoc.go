package output

// EntryDoc is used in the stop, add, attach, edit commands
type EntryDoc struct {
	BaseSchema
	Entry Entry `json:"entry"`
	BaseWarnings
}

func NewEntryDoc(e Entry, w []Warning) EntryDoc {
	return EntryDoc{BaseSchema: BaseSchema{SchemaVersion}, Entry: e, Warnings: ensureNotNil(w)}
}
