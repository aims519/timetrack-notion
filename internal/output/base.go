package output

// BaseSchema is embedded in all output
type BaseSchema struct {
	SchemaVersion int `json:"schema_version"`
}

const SchemaVersion = 1

// BaseWarnings is embedded in selected output
type BaseWarnings struct {
	Warnings []Warning `json:"warnings"`
}

type warningLister interface {
	listWarnings() []Warning
}

func (w BaseWarnings) listWarnings() []Warning {
	return w.Warnings
}
