package output

type Entry struct {
	ID          string   `json:"id"`
	Start       UTCTime  `json:"start"`
	End         *UTCTime `json:"end"`
	Seconds     *int     `json:"seconds"`
	Minutes     *int     `json:"minutes"`
	Description *string  `json:"description"`
	Task        *TaskRef `json:"task"`
	SyncState   string   `json:"sync_state"`
	SyncError   *string  `json:"sync_error"`
	Deleted     bool     `json:"deleted"`
}
