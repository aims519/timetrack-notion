package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

const (
	BaseURL          = "https://api.notion.com/v1"
	notionAPIVersion = "2026-03-11"
	queryPageSize    = 100
)

type Client struct {
	httpClient *http.Client
	apiToken   string
	apiVersion string
	baseURL    string
}

func NewClient(baseURL, apiToken string, httpClient *http.Client) *Client {
	return &Client{
		httpClient: httpClient,
		baseURL:    baseURL,
		apiToken:   apiToken,
		apiVersion: notionAPIVersion,
	}
}

func (c *Client) QueryDataSource(ctx context.Context, dataSourceID string, since *time.Time) ([]Page, error) {
	path := fmt.Sprintf("/data_sources/%s/query", dataSourceID)
	var pageResults []Page
	var nextCursor string
	moreResultsAvailable := true

	for moreResultsAvailable {
		pages, cursor, hasMore, err := c.dataSourcePageRequest(ctx, path, nextCursor, since)
		if err != nil {
			return nil, err
		}

		pageResults = append(pageResults, pages...)
		moreResultsAvailable = hasMore
		nextCursor = cursor
	}

	return pageResults, nil
}

// addDefaultHeaders adds the HTTP headers that all Notion API requests require
func addDefaultHeaders(req *http.Request, apiVersion, apiToken string) {
	req.Header.Add("Notion-Version", apiVersion)
	req.Header.Add("Authorization", "Bearer "+apiToken)
	req.Header.Add("Content-Type", "application/json")
}

// TooManyRequestsError is returned in the case of a 429 response from Notion's API
// with a number of seconds to wait before retrying. Callers can check for this error
// using errors.As and handle retries accordingly
type TooManyRequestsError struct {
	RetryAfterSeconds *int
}

func (e *TooManyRequestsError) Error() string {
	if e.RetryAfterSeconds != nil {
		return fmt.Sprintf("too many requests, wait %d seconds before retrying", *e.RetryAfterSeconds)
	}
	return "too many requests to Notion, no retry timing guidance available"
}

// ErrServerError is returned as the error cause for any response code >= 500
// Sync retry logic checks for this error and retries accordingly
var ErrServerError = errors.New("sync error: Notion server error")

func handleErrorResponse(res *http.Response, b []byte) error {
	if res.StatusCode == http.StatusOK {
		return nil
	}

	var e errorResponse
	var messageDetailSuffix string
	if err := json.Unmarshal(b, &e); err == nil && e.Message != "" {
		messageDetailSuffix = fmt.Sprintf(": %q", e.Message)
	}

	if res.StatusCode >= 500 {
		return &clierr.Error{Code: clierr.Internal, Message: "server error returned from Notion", Cause: ErrServerError}
	}

	switch res.StatusCode {
	case http.StatusBadRequest:
		return &clierr.Error{Code: clierr.Usage, Message: fmt.Sprintf("rejected request by Notion%v", messageDetailSuffix)}
	case http.StatusUnauthorized:
		return &clierr.Error{Code: clierr.AccessDenied, Message: fmt.Sprintf("invalid Notion API key%v", messageDetailSuffix)}
	case http.StatusNotFound:
		return &clierr.Error{Code: clierr.NotFound, Message: "object not found in Notion"}
	case http.StatusTooManyRequests:
		var retryAfterSeconds *int
		s, err := strconv.Atoi(res.Header.Get("Retry-After"))
		if err == nil && s > 0 {
			retryAfterSeconds = &s
		}
		return &clierr.Error{Code: clierr.Offline, Message: fmt.Sprintf("too many requests to Notion%v", messageDetailSuffix), Cause: &TooManyRequestsError{RetryAfterSeconds: retryAfterSeconds}}
	}

	return &clierr.Error{Code: clierr.Internal, Message: "unknown Notion error"}
}

func (c *Client) dataSourcePageRequest(ctx context.Context, path, startCursor string, since *time.Time) (pages []Page, nextCursor string, hasMore bool, err error) {
	type lastEditedTime struct {
		OnOrAfter string `json:"on_or_after"`
	}

	type timeFilter struct {
		TimeStamp      string         `json:"timestamp"`
		LastEditedTime lastEditedTime `json:"last_edited_time"`
	}

	type request struct {
		StartCursor string      `json:"start_cursor,omitempty"`
		PageSize    int         `json:"page_size"`
		TimeFilter  *timeFilter `json:"filter,omitempty"`
	}

	r := request{StartCursor: startCursor, PageSize: queryPageSize}
	if since != nil {
		sinceFormatted := since.UTC().Format(time.RFC3339)
		r.TimeFilter = &timeFilter{TimeStamp: "last_edited_time", LastEditedTime: lastEditedTime{OnOrAfter: sinceFormatted}}
	}
	b, err := json.Marshal(r)
	if err != nil {
		return []Page{}, "", false, &clierr.Error{Code: clierr.Internal, Message: "unable to communicate with Notion", Cause: err}
	}

	payload := bytes.NewReader(b)
	var qr queryResult
	if err := c.do(ctx, http.MethodPost, path, payload, &qr); err != nil {
		return []Page{}, "", false, err
	}

	return qr.Results, qr.NextCursor, qr.HasMore, nil
}

// LookupDataSourceID queries Notion for the data source ID associated with a database.
// If a database has more than one data source, an error is returned along with the
// candidates
func (c *Client) LookupDataSourceID(ctx context.Context, databaseID string) (*string, error) {
	type dataSource struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type database struct {
		ID          string       `json:"id"`
		DataSources []dataSource `json:"data_sources"`
	}

	path := fmt.Sprintf("/databases/%s", databaseID)
	var dbResult database
	if err := c.do(ctx, http.MethodGet, path, nil, &dbResult); err != nil {
		return nil, err
	}

	if len(dbResult.DataSources) == 0 {
		return nil, &clierr.Error{Code: clierr.NotFound, Message: fmt.Sprintf("no data source found for database, %q", databaseID)}
	}

	if len(dbResult.DataSources) > 1 {
		type dataSourceCandidate struct {
			ID   string `json:"data_source_id"`
			Name string `json:"name"`
		}

		var candidates []dataSourceCandidate
		for _, can := range dbResult.DataSources {
			candidates = append(candidates, dataSourceCandidate(can))
		}
		return nil, &clierr.Error{Code: clierr.AmbiguousSelector, Message: "database has more than one data source", Details: map[string]any{"selector": databaseID, "candidates": candidates}}
	}

	return &dbResult.DataSources[0].ID, nil
}

func (c *Client) GetDataSource(ctx context.Context, dataSourceID string) (DataSource, error) {
	path := fmt.Sprintf("/data_sources/%s", dataSourceID)

	var dsResult DataSource
	if err := c.do(ctx, http.MethodGet, path, nil, &dsResult); err != nil {
		return DataSource{}, err
	}

	return dsResult, nil
}

func (c *Client) CreateDatabase(ctx context.Context, parentPageID string, tasksDataSourceID string) (dataSourceID string, err error) {
	requestJSON := fmt.Sprintf(`
	{
		"parent": {
			"type": "page_id",
			"page_id": "%s"
		},
		"title": [
			{
			"text": {
				"content": "Time entries"
			},
			"type": "text"
			}
		],
		"description": [
			{
			"text": {
				"content": "Entries database, created and managed by tt"
			},
			"type": "text"
			}
		],
		"is_inline": false,
		"initial_data_source": {
			"properties": {
				"Title" : {
					"id": "title",
					"name": "Title",
					"type": "title",
					"title": {}
				},
				"Task" : {
					"id": "task",
					"type": "relation",
					"relation" : {
						"data_source_id": "%s",
						"type": "dual_property",
						"dual_property": {}
					}
				},
				"Start": {
					"id": "start",
					"name": "Start",
					"type": "date",
					"date": {}
				},
				"End": {
					"id": "end",
					"name": "End",
					"type": "date",
					"date": {}
				},
				"Description": {
					"id": "description",
					"name": "Description",
					"type": "rich_text",
					"rich_text": {}
				},
				"Local ID": {
					"id": "local_id",
					"name": "Local ID",
					"type": "rich_text",
					"rich_text": {}
				},
				"Minutes": {
					"id": "minutes",
					"name": "Minutes",
					"type": "formula",
					"formula": {
						"expression": "dateBetween(prop(\"End\"), prop(\"Start\"), \"minutes\")"
					}
				}
			}
		}
	}`, parentPageID, tasksDataSourceID)

	payload := strings.NewReader(requestJSON)

	type dataSource struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}

	type response struct {
		ID          string       `json:"id"`
		DataSources []dataSource `json:"data_sources"`
	}

	var resObj response
	if err := c.do(ctx, http.MethodPost, "/databases", payload, &resObj); err != nil {
		return "", err
	}

	if len(resObj.DataSources) != 1 {
		return "", &clierr.Error{Code: clierr.Internal, Message: "unable to parse Notion create database response: unexpected data source count"}
	}

	return resObj.DataSources[0].ID, nil
}

// do builds an HTTP request and sends to Notion. Unmarshals the response body
// to v if a non-nil pointer was provided
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, v any) error {
	url := fmt.Sprintf("%s%s", c.baseURL, path)

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "unable to build Notion request", Cause: err}
	}
	addDefaultHeaders(req, c.apiVersion, c.apiToken)

	res, err := c.httpClient.Do(req)
	if err != nil {
		cause := fmt.Errorf("%w: %w", ErrNetworkFailure, err)
		return &clierr.Error{Code: clierr.Offline, Message: "unable to communicate with Notion", Cause: cause}
	}
	defer res.Body.Close()

	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "unable to read response from Notion", Cause: err}
	}

	err = handleErrorResponse(res, respBody)
	if err != nil {
		return err
	}

	// no need to unmarshal if no pointer provided
	if v == nil {
		return nil
	}

	err = json.Unmarshal(respBody, v)
	if err != nil {
		return &clierr.Error{Code: clierr.Internal, Message: "unable to parse Notion response", Cause: err}
	}

	return nil
}
