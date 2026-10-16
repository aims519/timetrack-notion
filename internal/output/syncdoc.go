package output

type SyncResultState string

const (
	SyncResultSynced  SyncResultState = "synced"
	SyncResultFailed  SyncResultState = "failed"
	SyncResultBlocked SyncResultState = "blocked"
)

type SyncErrorCode string

const (
	SyncErrorCodeRateLimited  SyncErrorCode = "rate_limited"
	SyncErrorCodeServerError  SyncErrorCode = "server_error"
	SyncErrorCodeTaskNotFound SyncErrorCode = "task_not_found"
	SyncErrorCodeRowMissing   SyncErrorCode = "row_missing"
	SyncErrorCodeBadRequest   SyncErrorCode = "bad_request"
)

type SyncResultError struct {
	Code    SyncErrorCode `json:"code"`
	Message string        `json:"message"`
}

// SyncResult. Build using the constructors below
type SyncResult struct {
	EntryID string           `json:"entry_id"`
	State   SyncResultState  `json:"state"`
	Error   *SyncResultError `json:"error"`
}

func NewSyncedResult(entryID string) SyncResult {
	return SyncResult{EntryID: entryID, State: SyncResultSynced, Error: nil}
}

func NewFailedResult(entryID string, errorCode SyncErrorCode, errorMessage string) SyncResult {
	return SyncResult{EntryID: entryID, State: SyncResultFailed, Error: &SyncResultError{Code: errorCode, Message: errorMessage}}
}

func NewBlockedResult(entryID string, errorCode SyncErrorCode, errorMessage string) SyncResult {
	return SyncResult{EntryID: entryID, State: SyncResultBlocked, Error: &SyncResultError{Code: errorCode, Message: errorMessage}}
}

type SyncSummaryCounts struct {
	Synced       int `json:"synced"`
	Failed       int `json:"failed"`
	Blocked      int `json:"blocked"`
	Waiting      int `json:"waiting"`
	StillBlocked int `json:"still_blocked"`
}

// SyncDoc is used in the `sync` command
type SyncDoc struct {
	BaseSchema
	Results []SyncResult      `json:"results"`
	Summary SyncSummaryCounts `json:"summary"`
	BaseWarnings
}

func NewSyncDoc(r []SyncResult, waitingCount int, stillBlockedCount int, w []Warning) SyncDoc {
	// calculate summary values
	var synced, failed, blocked int
	for _, v := range r {
		switch v.State {
		case SyncResultSynced:
			synced++
		case SyncResultFailed:
			failed++
		case SyncResultBlocked:
			blocked++
		}
	}

	summary := SyncSummaryCounts{Synced: synced, Failed: failed, Blocked: blocked, Waiting: waitingCount, StillBlocked: stillBlockedCount}

	return SyncDoc{BaseSchema: BaseSchema{SchemaVersion}, Results: ensureNotNil(r), Summary: summary, Warnings: ensureNotNil(w)}
}
