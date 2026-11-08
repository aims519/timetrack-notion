package store

import "errors"

type AmbiguousTaskError struct {
	Candidates []Task
}

func (e *AmbiguousTaskError) Error() string {
	return "ambiguous results found in task lookup"
}

type AmbiguousEntryError struct {
	Candidates []Entry
}

func (e *AmbiguousEntryError) Error() string {
	return "ambiguous results found in entry lookup"
}

var ErrTaskNotFound = errors.New("no matching task")
