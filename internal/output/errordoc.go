package output

import "github.com/aims519/timetrack-notion/internal/clierr"

type ErrorDoc struct {
	BaseSchema
	Error clierr.Error `json:"error"`
}

func NewErrorDoc(e clierr.Error) ErrorDoc {
	return ErrorDoc{BaseSchema: BaseSchema{SchemaVersion}, Error: e}
}
