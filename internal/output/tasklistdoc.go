package output

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"
)

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

func (d TaskListDoc) WriteTable(w io.Writer, loc *time.Location) error {
	tabW := tabwriter.NewWriter(w, 0, 0, 3, ' ', tabwriter.TabIndent)
	if len(d.Tasks) == 0 {
		_, _ = fmt.Fprintf(tabW, "No tasks found\n")
		return tabW.Flush()
	}

	// headings
	_, _ = fmt.Fprintf(tabW, "Order\tShortID\tName\tDone\n")

	// rows
	for _, t := range d.Tasks {
		var orderStr = "-"
		if t.Order != nil {
			orderStr = strconv.FormatFloat(*t.Order, 'f', -1, 64)
		}

		_, _ = fmt.Fprintf(tabW, "%s\t%s\t%s\t%v\n", orderStr, t.ShortID, t.Name, t.Done)
	}

	return tabW.Flush()
}
