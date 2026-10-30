package output

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// Write writes doc to w in format f
func Write(w io.Writer, stdErr io.Writer, f Format, doc Doc, loc *time.Location) error {
	switch f {
	case Table:
		if l, ok := doc.(warningLister); ok {
			err := writeWarnings(stdErr, l)
			if err != nil {
				return err
			}
		}
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

func writeWarnings(stdErr io.Writer, l warningLister) error {
	for _, w := range l.listWarnings() {
		_, err := fmt.Fprintf(stdErr, "warning: %s\n", w.Message)
		if err != nil {
			return err
		}
	}

	return nil
}
