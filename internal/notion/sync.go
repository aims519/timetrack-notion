package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// ErrDataSourceUnusable is a sentinel error returned during a sync operation when
// a time entries data source request fails with a 404 or 400, so that the run can be aborted
var ErrDataSourceUnusable = errors.New("entries data source is unusable")

// obj is a type alias for a map, used to construct
// JSON requests
type obj = map[string]any

// textItems builds the array Notion expects for rich_text
// and title properties
func textItems(s string) []any {
	return []any{obj{"text": obj{"content": s}}}
}

// relationItems builds the array Notion expects for a
// relation property with a single relation
func relationItems(id string) []any {
	return []any{obj{"id": id}}
}

// CreateEntryPage attempts to add a time Entry Page in Notion, using the provided details
// - Start and End times are truncated to the minute and sent in UTC
// - Does not check for an existing page before sending the request, as this is handled elsewhere
func (c *Client) CreateEntryPage(ctx context.Context, dataSourceID, title, description, localID, taskPageID string, start, end time.Time) (Page, error) {
	payload, err := entryPagePayload(dataSourceID, title, description, localID, taskPageID, start, end)
	if err != nil {
		return Page{}, err
	}

	var p Page
	if err := c.do(ctx, http.MethodPost, "/pages", payload, &p); err != nil {
		return Page{}, err
	}

	return p, nil
}

// FindEntryPage checks whether a page has already been created for an entry, by
// querying the entries data source for a page with a matching Local ID. If the data source
// is not found or in the event of a bad request, a wrapped ErrDataSourceUnusable error is returned.
// If the page is found, the page object is returned with a true result
func (c *Client) FindEntryPage(ctx context.Context, entriesDataSourceID, localIDKey, localIDValue string) (Page, bool, error) {
	body := obj{
		"page_size": 1,
		"filter": obj{
			"type":      "rich_text",
			"property":  localIDKey,
			"rich_text": obj{"equals": localIDValue},
		},
	}

	b, err := json.Marshal(body)
	if err != nil {
		return Page{}, false, &clierr.Error{Code: clierr.Internal, Message: "unable to communicate with Notion", Cause: err}
	}

	payload := bytes.NewReader(b)
	path := fmt.Sprintf("/data_sources/%s/query", entriesDataSourceID)
	var qr queryResult
	err = c.do(ctx, http.MethodPost, path, payload, &qr)
	var cliErr *clierr.Error
	if errors.As(err, &cliErr) {
		if cliErr.Code == clierr.NotFound {
			return Page{}, false, &clierr.Error{Code: clierr.NotFound, Message: "saved entries data source not found in Notion, restore or re-share", Cause: ErrDataSourceUnusable}
		}
		if cliErr.Code == clierr.Usage {
			return Page{}, false, &clierr.Error{Code: clierr.Usage, Message: "entry page data source query rejected by Notion, check the Local ID property exists and is a text property", Cause: ErrDataSourceUnusable}
		}
	}
	if err != nil {
		return Page{}, false, err
	}

	page := Page{}
	found := false
	if len(qr.Results) > 0 {
		found = true
		page = qr.Results[0]
	}

	return page, found, nil
}

// UpdateEntryPage sends a PATCH request to Notion to update a Page in the Time entries data source
func (c *Client) UpdateEntryPage(ctx context.Context, pageID, dataSourceID, title, description, localID, taskPageID string, start, end time.Time) (Page, error) {
	payload, err := entryPagePayload(dataSourceID, title, description, localID, taskPageID, start, end)
	if err != nil {
		return Page{}, err
	}

	var p Page
	path := fmt.Sprintf("/pages/%s", pageID)
	if err := c.do(ctx, http.MethodPatch, path, payload, &p); err != nil {
		return Page{}, err
	}

	return p, nil
}

// entryPagePayload builds and returns a JSON payload to be used in POST/PATCH requests
// to Notion. Start and end times are truncated to the minute
func entryPagePayload(dataSourceID, title, description, localID, taskPageID string, start, end time.Time) (*bytes.Reader, error) {
	startTruncated := start.UTC().Truncate(time.Minute).Format(time.RFC3339)
	endTruncated := end.UTC().Truncate(time.Minute).Format(time.RFC3339)

	body := obj{
		"parent": obj{"type": "data_source_id", "data_source_id": dataSourceID},
		"properties": obj{
			"Title":       obj{"type": "title", "title": textItems(title)},
			"Local ID":    obj{"type": "rich_text", "rich_text": textItems(localID)},
			"Start":       obj{"type": "date", "date": obj{"start": startTruncated}},
			"End":         obj{"type": "date", "date": obj{"start": endTruncated}},
			"Description": obj{"type": "rich_text", "rich_text": textItems(description)},
			"Task":        obj{"type": "relation", "relation": relationItems(taskPageID)},
		},
		"in_trash": false,
	}

	b, err := json.Marshal(body)
	if err != nil {
		return nil, &clierr.Error{Code: clierr.Internal, Message: "unable to marshal entry page JSON", Cause: err}
	}

	return bytes.NewReader(b), nil
}

// TrashEntryPage updates a Time entries row to trashed. If the page is already trashed,
// Notion returns a 400 response
func (c *Client) TrashEntryPage(ctx context.Context, pageID, dataSourceID string) (Page, error) {
	b, err := json.Marshal(obj{
		"in_trash": true,
	})
	if err != nil {
		return Page{}, &clierr.Error{Code: clierr.Internal, Message: "unable to communicate with Notion", Cause: err}
	}

	var p Page
	path := fmt.Sprintf("/pages/%s", pageID)
	payload := bytes.NewReader(b)
	if err := c.do(ctx, http.MethodPatch, path, payload, &p); err != nil {
		return Page{}, err
	}

	return p, nil
}

// EntryPage returns a Time entries Page from Notion
func (c *Client) EntryPage(ctx context.Context, pageID, dataSourceID string) (Page, error) {
	var p Page
	path := fmt.Sprintf("/pages/%s", pageID)
	if err := c.do(ctx, http.MethodGet, path, nil, &p); err != nil {
		return Page{}, err
	}

	return p, nil
}
