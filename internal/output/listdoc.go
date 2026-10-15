package output

// ListDoc is used in the list command
type ListDoc struct {
	BaseSchema
	Entries      []Entry `json:"entries"`
	TotalMinutes int     `json:"total_minutes"`
	Truncated    bool    `json:"truncated"`
	BaseWarnings
}

func NewListDoc(entries []Entry, totalMinutes int, truncated bool, w []Warning) ListDoc {
	return ListDoc{BaseSchema: BaseSchema{SchemaVersion}, Entries: ensureNotNil(entries), TotalMinutes: totalMinutes, Truncated: truncated, Warnings: ensureNotNil(w)}
}
