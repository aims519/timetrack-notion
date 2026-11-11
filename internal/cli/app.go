package cli

import (
	"net/http"

	"github.com/aims519/timetrack-notion/internal/output"
	"github.com/aims519/timetrack-notion/internal/store"
)

// app holds common properties needed by every tt command. Holds a pointer to the db connection pool so that Run can safely close it
type app struct {
	db               *store.Store
	format           *output.Format
	notionBaseURL    string
	notionHTTPClient *http.Client
	noInput          bool
	dbFilePath       string
}
