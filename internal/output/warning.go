package output

type WarningCode string

const (
	Overlap                 WarningCode = "overlap"
	TimeResolvedToYesterday WarningCode = "time_resolved_to_yesterday"
	AmbiguousLocalTime      WarningCode = "ambiguous_local_time"
	StaleTimer              WarningCode = "stale_timer"
	TaskCacheStale          WarningCode = "task_cache_stale"
	SyncNote                WarningCode = "sync_note"
)

type Warning struct {
	Code    WarningCode    `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"` // Can be nil, don't need a pointer
}
