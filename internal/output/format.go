package output

import "fmt"

// Format selects how CLI output should be rendered
type Format string

func (f *Format) Set(s string) error {
	switch Format(s) {
	case Table, JSON:
		*f = Format(s)
		return nil
	}
	return fmt.Errorf("must be %q or %q", Table, JSON)
}

func (f *Format) String() string {
	return string(*f)
}

func (f *Format) Type() string {
	return "format"
}

const (
	// Table renders output in human readable format
	Table Format = "table"

	// JSON renders output in agent-friendly format
	JSON Format = "json"
)
