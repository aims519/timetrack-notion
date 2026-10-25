package output

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Write writes doc to w in format f
func Write(w io.Writer, f Format, doc Doc, loc *time.Location) error {
	switch f {
	case Table:
		return doc.WriteTable(w, loc)
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)

		// Encoder adds the trailing \n automatically
		return enc.Encode(doc)
	default:
		return fmt.Errorf("unknown format %q", f)
	}
}
