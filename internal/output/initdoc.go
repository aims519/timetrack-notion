package output

import (
	"fmt"
	"io"
	"time"
)

type InitDoc struct {
	BaseSchema
	NotionContext NotionContext `json:"context"`
	BaseWarnings
}

func NewInitDoc(c NotionContext, w []Warning) InitDoc {
	return InitDoc{
		BaseSchema:    BaseSchema{SchemaVersion},
		NotionContext: c,
		Warnings:      ensureNotNil(w),
	}
}

func (d InitDoc) WriteTable(w io.Writer, loc *time.Location) error {
	_, err := fmt.Fprintf(w, "TODO INIT OUTPUT")
	if err != nil {
		return err
	}

	return nil
}
