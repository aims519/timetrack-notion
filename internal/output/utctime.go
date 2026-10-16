package output

import (
	"fmt"
	"time"
)

// UTCTime enforces UTC-formatted time with truncated nanoseconds for output types
type UTCTime struct {
	t time.Time
}

func NewUTCTime(t time.Time) UTCTime {
	t = t.UTC()
	return UTCTime{t}
}

func (utc UTCTime) MarshalJSON() ([]byte, error) {
	formattedTime := utc.t.Format(time.RFC3339)
	return fmt.Appendf(nil, "%q", formattedTime), nil
}
