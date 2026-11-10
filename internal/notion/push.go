package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

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
	url := fmt.Sprintf("%s/pages", c.baseURL)

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
	}

	b, err := json.Marshal(body)
	if err != nil {
		return Page{}, &clierr.Error{Code: clierr.Internal, Message: "unable to communicate with Notion", Cause: err}
	}

	payload := bytes.NewReader(b)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, payload)
	if err != nil {
		return Page{}, &clierr.Error{Code: clierr.Internal, Message: "unable to communicate with Notion", Cause: err}
	}
	addDefaultHeaders(req, c.apiVersion, c.apiToken)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return Page{}, &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion", Cause: err}
	}
	defer res.Body.Close()

	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return Page{}, &clierr.Error{Code: clierr.Internal, Message: "unable to read response from Notion", Cause: err}
	}

	err = handleErrorResponse(res, bytes)
	if err != nil {
		return Page{}, err
	}

	var p Page
	err = json.Unmarshal(bytes, &p)
	if err != nil {
		return Page{}, &clierr.Error{Code: clierr.Internal, Message: "unable to parse Notion response", Cause: err}
	}

	return p, nil
}

// FindEntryPage checks whether a page has already been created for an entry, by
// querying a datasource and filtering by its localID. If found, the page object is returned with a true result
func (c *Client) FindEntryPage(ctx context.Context, entriesDataSourceID, localIDKey, localIDValue string) (Page, bool, error) {
	url := fmt.Sprintf("%s/data_sources/%s/query", c.baseURL, entriesDataSourceID)

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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, payload)
	if err != nil {
		return Page{}, false, &clierr.Error{Code: clierr.Internal, Message: "unable to communicate with Notion", Cause: err}
	}
	addDefaultHeaders(req, c.apiVersion, c.apiToken)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return Page{}, false, &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion", Cause: err}
	}
	defer res.Body.Close()

	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return Page{}, false, &clierr.Error{Code: clierr.Internal, Message: "unable to read response from Notion", Cause: err}
	}

	err = handleErrorResponse(res, bytes)
	if err != nil {
		return Page{}, false, err
	}

	var qr queryResult
	err = json.Unmarshal(bytes, &qr)
	if err != nil {
		return Page{}, false, &clierr.Error{Code: clierr.Internal, Message: "unable to parse Notion response", Cause: err}
	}

	page := Page{}
	found := false
	if len(qr.Results) > 0 {
		found = true
		page = qr.Results[0]
	}

	return page, found, nil
}
