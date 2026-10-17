package output

import "io"

// Doc is an output document that can also render itself as a table
type Doc interface {
	WriteTable(w io.Writer) error
}
