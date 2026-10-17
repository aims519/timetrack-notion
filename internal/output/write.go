package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// Write writes doc to w in format f
func Write(w io.Writer, f Format, doc Doc) error {
	switch f {
	case Table:
		return doc.WriteTable(w)
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)

		// Encoder adds the trailing \n automatically
		return enc.Encode(doc)
	default:
		return fmt.Errorf("unknown format %q", f)
	}
}
