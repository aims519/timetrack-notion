package output

type WarningCode string

const (
	Overlap              WarningCode = "overlap"
	StaleTimer           WarningCode = "stale_timer"
	TaskCacheStale       WarningCode = "task_cache_stale"
	OrderPropertyMissing WarningCode = "order_property_missing"
)

type Warning struct {
	Code    WarningCode    `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"` // Can be nil, don't need a pointer
}
