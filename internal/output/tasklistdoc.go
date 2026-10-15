package output

// TaskListDoc is used in the `task list` command
type TaskListDoc struct {
	BaseSchema
	Tasks     []TaskItem `json:"tasks"`
	Truncated bool       `json:"truncated"`
	BaseWarnings
}

func NewTaskListDoc(tasks []TaskItem, truncated bool, w []Warning) TaskListDoc {
	return TaskListDoc{BaseSchema: BaseSchema{SchemaVersion}, Tasks: ensureNotNil(tasks), Truncated: truncated, Warnings: ensureNotNil(w)}
}
