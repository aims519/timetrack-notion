package output

type TodayCounts struct {
	Entries int `json:"entries"`
	Minutes int `json:"minutes"`
}

type CountsSummary struct {
	Pending int `json:"pending"`
	Blocked int `json:"blocked"`
	Waiting int `json:"waiting"`
}

// StatusDoc used in status command
type StatusDoc struct {
	BaseSchema
	Running        *Entry        `json:"running"`
	ElapsedSeconds *int          `json:"elapsed_seconds"`
	Today          TodayCounts   `json:"today"`
	Counts         CountsSummary `json:"counts"`
	Connected      bool          `json:"connected"`
	BaseWarnings
}

func NewStatusDoc(running *Entry, elapsedSeconds int, today TodayCounts, counts CountsSummary, connected bool, w []Warning) StatusDoc {
	var computedSeconds *int
	if running != nil {
		computedSeconds = &elapsedSeconds
	}
	return StatusDoc{BaseSchema: BaseSchema{SchemaVersion}, Running: running, ElapsedSeconds: computedSeconds, Today: today, Counts: counts, Connected: connected, Warnings: ensureNotNil(w)}
}
