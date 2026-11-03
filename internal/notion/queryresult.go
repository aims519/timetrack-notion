package notion

import (
	"time"
)

type queryResult struct {
	PageType      string            `json:"type"`
	NextCursor    string            `json:"next_cursor"`
	HasMore       bool              `json:"has_more"`
	Results       []Page            `json:"results"`
	RequestStatus queryResultStatus `json:"request_status"`
}

type queryResultStatus struct {
	Type             string `json:"type"`
	IncompleteReason string `json:"incomplete_reason"`
}

// PageProperty represents one of several possible property types.
// The correct nullable value should be selected based on the type.
type PageProperty struct {
	Type     string     `json:"type"`
	Title    []RichText `json:"title"`
	Relation []Relation `json:"relation"`
	Number   *float64   `json:"number"`
	Checkbox bool       `json:"checkbox"`
}

type RichText struct {
	PlainText string `json:"plain_text"`
}

type Relation struct {
	ID string `json:"id"`
}

type Page struct {
	ID                string                  `json:"id"`
	CreatedTimeUTC    time.Time               `json:"created_time"`
	LastEditedTimeUTC time.Time               `json:"last_edited_time"`
	InTrash           bool                    `json:"in_trash"`
	IsArchived        bool                    `json:"is_archived"`
	IsLocked          bool                    `json:"is_locked"`
	Properties        map[string]PageProperty `json:"properties"`
}
