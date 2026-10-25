package output

import (
	"io"
	"time"
)

// Doc is an output document that can also render itself as a table
type Doc interface {
	WriteTable(w io.Writer, loc *time.Location) error
}
