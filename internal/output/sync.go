package output

type SyncResult string

// Prefixed because entries' sync states reuse several of these words
const (
	SyncResultSynced   SyncResult = "synced"
	SyncResultPending  SyncResult = "pending"
	SyncResultFailed   SyncResult = "failed"
	SyncResultBlocked  SyncResult = "blocked"
	SyncResultWaiting  SyncResult = "waiting"
	SyncResultDisabled SyncResult = "disabled"
)

type Sync struct {
	Attempted bool       `json:"attempted"`
	Result    SyncResult `json:"result"`
	Pending   int        `json:"pending"`
}
