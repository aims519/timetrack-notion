package output

// StartDoc is used by the start command
type StartDoc struct {
	BaseSchema
	Entry   Entry  `json:"entry"`
	Stopped *Entry `json:"stopped"`
	BaseWarnings
}

func NewStartDoc(e Entry, stopped *Entry, w []Warning) StartDoc {
	w = normaliseWarnings(w)
	return StartDoc{BaseSchema: BaseSchema{SchemaVersion}, Entry: e, Stopped: stopped, Warnings: w}
}
