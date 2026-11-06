package notion

import (
	"time"
)

// Common
type RichText struct {
	PlainText string `json:"plain_text"`
}

type Relation struct {
	ID           string  `json:"id"`
	DataSourceID *string `json:"data_source_id"`
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

type Page struct {
	ID                string                  `json:"id"`
	CreatedTimeUTC    time.Time               `json:"created_time"`
	LastEditedTimeUTC time.Time               `json:"last_edited_time"`
	InTrash           bool                    `json:"in_trash"`
	IsArchived        bool                    `json:"is_archived"`
	IsLocked          bool                    `json:"is_locked"`
	Properties        map[string]PageProperty `json:"properties"`
}

// DataSource
type DataSourceTitle struct {
	Type      string `json:"type"`
	PlainText string `json:"plain_text"`
}

type DataSourceRelation struct {
	DataSourceID string `json:"data_source_id"`
}

type DataSourceProperty struct {
	Name     string             `json:"name"`
	Type     string             `json:"type"`
	Relation DataSourceRelation `json:"relation"`
}

type DataSource struct {
	ID         string                        `json:"id"`
	Title      []DataSourceTitle             `json:"title"`
	Properties map[string]DataSourceProperty `json:"properties"`
}
