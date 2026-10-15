package output

// EntryDoc is used in the stop, add, attach, edit commands
type EntryDoc struct {
	BaseSchema
	Entry Entry `json:"entry"`
	Sync  Sync  `json:"sync"`
	BaseWarnings
}

func NewEntryDoc(e Entry, s Sync, w []Warning) EntryDoc {
	if w == nil {
		w = []Warning{}
	}
	return EntryDoc{BaseSchema: BaseSchema{SchemaVersion}, Entry: e, Sync: s, Warnings: w}
}
