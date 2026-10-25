package store

import "time"

type Entry struct {
	ID          string
	Description *string
	StartUTC    time.Time
	EndUTC      *time.Time
	TaskPageID  *string
	EntryPageID *string
	SyncState   string
	UpdatedAt   time.Time
}
