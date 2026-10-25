package output

import (
	"fmt"
	"io"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

type ErrorDoc struct {
	BaseSchema
	Error clierr.Error `json:"error"`
}

func NewErrorDoc(e clierr.Error) ErrorDoc {
	return ErrorDoc{BaseSchema: BaseSchema{SchemaVersion}, Error: e}
}

// Write tabular output
func (d ErrorDoc) WriteTable(w io.Writer, _ *time.Location) error {
	_, err := fmt.Fprintln(w, d.Error.Message)
	return err
}
