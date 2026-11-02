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

const (
	notionBaseURL    = "https://api.notion.com/v1"
	notionAPIVersion = "2026-03-11"
	queryPageSize    = 20
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
	url := fmt.Sprintf("%s/data_sources/%s/query", c.baseURL, dataSourceID)
	var pageResults []Page
	var nextCursor string
	moreResultsAvailable := true

	for moreResultsAvailable {
		pages, cursor, hasMore, err := dataSourcePageRequest(ctx, url, nextCursor, c.apiVersion, c.apiToken, c.httpClient, since)
		if err != nil {
			return nil, err
		}

		pageResults = append(pageResults, pages...)
		moreResultsAvailable = hasMore
		nextCursor = cursor
	}

	return pageResults, nil
}

func addDefaultHeaders(req *http.Request, apiVersion, apiToken string) {
	req.Header.Add("Notion-Version", apiVersion)
	req.Header.Add("Authorization", "Bearer "+apiToken)
	req.Header.Add("Content-Type", "application/json")
}

func handleErrorResponse(res *http.Response, b []byte) error {
	if res.StatusCode == http.StatusOK {
		return nil
	}

	var e errorResponse
	var messageDetailSuffix string
	if err := json.Unmarshal(b, &e); err == nil && e.Message != "" {
		messageDetailSuffix = fmt.Sprintf(": %q", e.Message)
	}

	switch res.StatusCode {
	case http.StatusBadRequest:
		return &clierr.Error{Code: clierr.Usage, Message: fmt.Sprintf("rejected request by Notion%v", messageDetailSuffix)}
	case http.StatusUnauthorized:
		return &clierr.Error{Code: clierr.AccessDenied, Message: fmt.Sprintf("Invalid Notion API key%v", messageDetailSuffix)}
	case http.StatusNotFound:
		return &clierr.Error{Code: clierr.NotFound, Message: "object not found in Notion"}
	case http.StatusTooManyRequests:
		return &clierr.Error{Code: clierr.Usage, Message: "too many Notion requests"}
	case http.StatusInternalServerError:
		return &clierr.Error{Code: clierr.Internal, Message: "unexpected Notion error"}
	}

	return &clierr.Error{Code: clierr.Internal, Message: "unknown Notion error"}
}

func dataSourcePageRequest(ctx context.Context, url, startCursor, apiVersion, apiToken string, client *http.Client, since *time.Time) (pages []Page, nextCursor string, hasMore bool, err error) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, payload)
	if err != nil {
		return []Page{}, "", false, &clierr.Error{Code: clierr.Internal, Message: "unable to communicate with Notion", Cause: err}
	}
	addDefaultHeaders(req, apiVersion, apiToken)

	res, err := client.Do(req)
	if err != nil {
		return []Page{}, "", false, &clierr.Error{Code: clierr.Internal, Message: "unable to communicate with Notion", Cause: err}
	}
	defer res.Body.Close()

	bytes, err := io.ReadAll(res.Body)
	if err != nil {
		return []Page{}, "", false, &clierr.Error{Code: clierr.Internal, Message: "unable to read response from Notion", Cause: err}
	}

	err = handleErrorResponse(res, bytes)
	if err != nil {
		return []Page{}, "", false, err
	}

	var qr queryResult
	err = json.Unmarshal(bytes, &qr)
	if err != nil {
		return []Page{}, "", false, &clierr.Error{Code: clierr.Internal, Message: "unable to parse Notion response", Cause: err}
	}

	return qr.Results, qr.NextCursor, qr.HasMore, nil
}
