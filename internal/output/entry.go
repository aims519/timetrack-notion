package output

// Entry is a time entry in command output.
type Entry struct {
	ID    string   `json:"id"`
	Start UTCTime  `json:"start"`
	End   *UTCTime `json:"end"`

	// Seconds is the entry's real length, rounded to the nearest second. Null while the entry is running
	Seconds *int `json:"seconds"`

	// Minutes is (End-Start) after truncating both to the minute, matching Notion's truncation. Null while entry is running.
	Minutes     *int     `json:"minutes"`
	Description *string  `json:"description"`
	Task        *TaskRef `json:"task"`
	SyncState   string   `json:"sync_state"`
	SyncError   *string  `json:"sync_error"`
	Deleted     bool     `json:"deleted"`
}
