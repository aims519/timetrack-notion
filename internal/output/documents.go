package output

// EntryDoc is used in the stop, add, attach, edit commands
type EntryDoc struct {
	BaseSchema
	Entry Entry `json:"entry"`
	BaseWarnings
}

func NewEntryDoc(e Entry, w []Warning) EntryDoc {
	w = normaliseWarnings(w)
	return EntryDoc{BaseSchema: BaseSchema{SchemaVersion}, Entry: e, Warnings: w}
}

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

type DeleteAction string

const (
	DeleteActionRemove    DeleteAction = "remove"
	DeleteActionTombstone DeleteAction = "tombstone"
)

type DeleteDoc struct {
	BaseSchema
	DryRun  bool         `json:"dry_run"`
	Action  DeleteAction `json:"action"`
	Entry   Entry        `json:"entry"`
	Removed *bool        `json:"removed"`
	BaseWarnings
}

func NewDeleteDoc(dryRun bool, a DeleteAction, e Entry, w []Warning) DeleteDoc {
	w = normaliseWarnings(w)

	var removed *bool
	// True if action is remove. Nil on dry run
	if !dryRun {
		removed = new(a == DeleteActionRemove)
	}

	return DeleteDoc{BaseSchema: BaseSchema{SchemaVersion}, DryRun: dryRun, Action: a, Entry: e, Removed: removed, Warnings: w}
}

func normaliseWarnings(w []Warning) []Warning {
	if w == nil {
		w = []Warning{}
	}
	return w
}
