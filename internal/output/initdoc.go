package output

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

var rollupStepInstructions = []string{
	"In the tasks database, add a property, set its type to Rollup and name it \"Tracked (min)\"",
	"Click the new property's header and choose Edit property if the settings aren't already open",
	"Set Relation to the Time entries relation",
	"Set Property to Minutes",
	"Set Calculate to Sum",
}

type InitDoc struct {
	BaseSchema
	NotionContext     NotionContext `json:"context"`
	WaitingEntryCount int           `json:"waiting"`
	RollupSteps       []string      `json:"rollup_steps"`
	BaseWarnings
}

func NewInitDoc(c NotionContext, waitingEntryCount int, w []Warning) InitDoc {
	return InitDoc{
		BaseSchema:        BaseSchema{SchemaVersion},
		NotionContext:     c,
		WaitingEntryCount: waitingEntryCount,
		RollupSteps:       rollupStepInstructions,
		Warnings:          ensureNotNil(w),
	}
}

func (d InitDoc) WriteTable(w io.Writer, loc *time.Location) error {
	tabW := tabwriter.NewWriter(w, 0, 0, 3, ' ', tabwriter.TabIndent)

	_, _ = fmt.Fprintf(tabW, "Connected to Notion\n")
	_, _ = fmt.Fprintf(tabW, "Tasks\t%s\n", *d.NotionContext.TasksDataSourceID)
	_, _ = fmt.Fprintf(tabW, "Projects\t%s\n", *d.NotionContext.ProjectsDataSourceID)
	_, _ = fmt.Fprintf(tabW, "Time entries\t%s\n", *d.NotionContext.EntriesDataSourceID)

	if d.NotionContext.DefaultProjectPageID != nil {
		_, _ = fmt.Fprintf(tabW, "Default project\t%s\n\n", *d.NotionContext.DefaultProjectPageID)
	} else {
		_, _ = fmt.Fprintf(tabW, "Default project\tnone\n\n")
	}

	if d.WaitingEntryCount > 0 {
		_, _ = fmt.Fprintf(tabW, "%s waiting: use tt attach --entry to assign tasks\n\n", formatPlural(d.WaitingEntryCount, "entry", "entries"))
	}

	_, _ = fmt.Fprint(tabW, "To total tracked time on your tasks (optional):\n")
	for i, s := range d.RollupSteps {
		_, _ = fmt.Fprintf(tabW, "%d. %s\n", (i + 1), s)
	}

	return tabW.Flush()
}
