package output

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
