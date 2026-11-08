package store

import "errors"

// AmbiguousTaskError is returned when the store finds more than one task matching the given
// selector. The CLI then uses the Candidates to construct the final error
type AmbiguousTaskError struct {
	Candidates []Task
}

func (e *AmbiguousTaskError) Error() string {
	return "ambiguous results found in task lookup"
}

// AmbiguousEntryError is returned when the store finds more than one entry matching the given
// selector. The CLI then uses the Candidates to construct the final error
type AmbiguousEntryError struct {
	Candidates []Entry
}

func (e *AmbiguousEntryError) Error() string {
	return "ambiguous results found in entry lookup"
}

// ErrTaskNotFound is returned when the store cannot find a task matching the provided selector.
// The CLI can detect this error and construct the final error with the raw selector
var ErrTaskNotFound = errors.New("no matching task")
