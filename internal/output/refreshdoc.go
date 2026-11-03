package output

import (
	"fmt"
	"io"
	"time"
)

type RefreshDoc struct {
	BaseSchema
	Full              bool    `json:"full"`
	RefreshedAt       UTCTime `json:"refreshed_at"`
	Fetched           int     `json:"fetched"`
	Gone              *int    `json:"gone"`
	RefreshDurationMS int64   `json:"duration_ms"`
	BaseWarnings
}

func NewRefreshDoc(full bool, at time.Time, d time.Duration, fetched int, gone *int, w []Warning) RefreshDoc {
	return RefreshDoc{
		SchemaVersion:     SchemaVersion,
		Full:              full,
		RefreshedAt:       NewUTCTime(at),
		RefreshDurationMS: d.Milliseconds(),
		Fetched:           fetched,
		Gone:              gone,
		Warnings:          ensureNotNil(w),
	}
}

func (s RefreshDoc) WriteTable(w io.Writer, loc *time.Location) error {
	dur := time.Duration(s.RefreshDurationMS) * time.Millisecond
	if s.Full && s.Gone != nil {
		_, err := fmt.Fprintf(w, "Refreshed %s in %v, %d gone\n", formatPlural(s.Fetched, "task", "tasks"), formatMilliseconds(dur), *s.Gone)
		return err
	}

	_, err := fmt.Fprintf(w, "Refreshed %s in %v\n", formatPlural(s.Fetched, "task", "tasks"), formatMilliseconds(dur))
	return err
}
