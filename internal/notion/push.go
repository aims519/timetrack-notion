package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// CreateEntryPage attempts to add a time Entry Page in Notion, using the provided details
func (c *Client) CreateEntryPage(ctx context.Context, dataSourceID, title, description, localID, taskPageID string, start, end time.Time) (Page, error) {
	url := fmt.Sprintf("%s/pages", c.baseURL)

	startTruncated := start.UTC().Truncate(time.Minute).Format(time.RFC3339)
	endTruncated := end.UTC().Truncate(time.Minute).Format(time.RFC3339)

	requestJSON := fmt.Sprintf(`
	{	
		"parent": {
				"data_source_id": %s,
				"type": "data_source_id"
		},
		"properties": {
			"Title": {
				"id": "title",
				"type": "title",
				"title": [
					{
						"text": {
							"content": %s
						}
					}
				]
			},
			"Local ID": {
				"type": "rich_text",
				"rich_text": [
					{
						"text": {
							"content": %s
						}
					}
				]
			},
			"Start": {
				"type": "date",
				"date": {
					"start": %s
				} 
			},
			"End": {
				"type": "date",
					"date": {
					"start": %s
				} 
			},
			"Description": {
				"type": "rich_text",
				"rich_text": [
					{
						"text": {
							"content": %s
						}
					}
				]
			},
			"Task": {
				"type": "relation",
				"relation": [
					{
						"id": %s
					}
				]
			}
		}
	}
	`,
		jsonString(dataSourceID),
		jsonString(title),
		jsonString(localID),
		jsonString(startTruncated),
		jsonString(endTruncated),
		jsonString(description),
		jsonString(taskPageID))

	payload := strings.NewReader(requestJSON)
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
// querying a datasource and filtering by its localID. If found, the page object is returned
func (c *Client) FindEntryPage(ctx context.Context, entriesDataSourceID, localIDKey, localIDValue string) (Page, bool, error) {
	url := fmt.Sprintf("%s/data_sources/%s/query", c.baseURL, entriesDataSourceID)

	requestJSON := fmt.Sprintf(`
	{
		"page_size": 1,
		"filter": {
			"rich_text": {
				"equals": %s
			},
			"property": %s,
			"type": "rich_text"
		}
	}
	`, jsonString(localIDValue), jsonString(localIDKey))

	payload := strings.NewReader(requestJSON)
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

// jsonString marshalls the given user input so that
// it can be properly escaped in a JSON request body
func jsonString(input string) string {
	// error ignored since marshalling a string can't fail
	b, _ := json.Marshal(input)
	return string(b)
}
