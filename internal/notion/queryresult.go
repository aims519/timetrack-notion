package notion

import (
	"time"
)

type queryResult struct {
	PageType      string            `json:"type"`
	NextCursor    string            `json:"next_cursor"`
	HasMore       bool              `json:"has_more"`
	Results       []page            `json:"results"`
	RequestStatus queryResultStatus `json:"request_status"`
}

type queryResultStatus struct {
	Type             string `json:"type"`
	IncompleteReason string `json:"incomplete_reason"`
}

type page struct {
	ID                string    `json:"id"`
	CreatedTimeUTC    time.Time `json:"created_time"`
	LastEditedTimeUTC time.Time `json:"last_edited_time"`
	InTrash           bool      `json:"in_trash"`
	IsArchived        bool      `json:"is_archived"`
	IsLocked          bool      `json:"is_locked"`
}
