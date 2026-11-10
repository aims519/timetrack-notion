package notion

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

// entryPageText is one rich text item, as used by title and rich_text properties
type entryPageText struct {
	Text struct {
		Content string `json:"content"`
	} `json:"text"`
}

// entryPageRequest is the part of a create page request the tests check
type entryPageRequest struct {
	Parent struct {
		Type         string `json:"type"`
		DataSourceID string `json:"data_source_id"`
	} `json:"parent"`
	Properties map[string]struct {
		Type     string          `json:"type"`
		Title    []entryPageText `json:"title"`
		RichText []entryPageText `json:"rich_text"`
		Date     *struct {
			Start string `json:"start"`
			// A pointer, so a missing key (nil) can be told apart from an empty one
			TimeZone *string `json:"time_zone"`
		} `json:"date"`
		Relation []struct {
			ID string `json:"id"`
		} `json:"relation"`
	} `json:"properties"`
}

// newEntryPageServer returns a fake Notion that records each create request's body in got and
// answers with an entry page. A body that isn't valid JSON fails the test
func newEntryPageServer(t *testing.T, got *entryPageRequest) *httptest.Server {
	t.Helper()

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(got); err != nil {
			t.Errorf("request body is not valid JSON: %v", err)
			writeBadRequest(t, w, "invalid request body")
			return
		}

		if _, err := w.Write([]byte(`{"object": "page", "id": "entry-page-1"}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	return s
}

func TestCreateEntryPage_ReturnsPage(t *testing.T) {
	const want = "entry-page-1"

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responseBody := `{
			"object": "page",
			"id": "entry-page-1",
			"created_time": "2026-11-10T09:30:00.000Z",
			"last_edited_time": "2026-11-10T09:30:00.000Z",
			"in_trash": false,
			"properties": {
				"Title": {"id": "title", "type": "title", "title": [{"plain_text": "Writing tests"}]}
			}
		}`

		if _, err := w.Write([]byte(responseBody)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, "test-token", s.Client())

	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	got, err := nc.CreateEntryPage(t.Context(), "entries-ds", "Writing tests", "Writing tests", "local-1", "task-page", start, start.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.ID != want {
		t.Errorf("page ID = %q, want %q", got.ID, want)
	}
}

// The property names and types come from DESIGN.md §5 step 6, not from the client, so a change to
// either is caught here
func TestCreateEntryPage_RequestShape(t *testing.T) {
	const (
		dataSourceID = "entries-ds"
		title        = "Writing tests"
		description  = "Tests for CreateEntryPage"
		localID      = "0b9e6c3a-1f2d-4e5a-8b7c-6d5e4f3a2b1c"
		taskPageID   = "task-page"
		token        = "test-token"
	)

	wantHeaders := map[string]string{
		"Authorization":  "Bearer " + token,
		"Notion-Version": "2026-03-11",
		"Content-Type":   "application/json",
	}
	// Minutes is a formula, so Notion works it out and it is never sent
	wantPropertyTypes := map[string]string{
		"Title":       "title",
		"Task":        "relation",
		"Start":       "date",
		"End":         "date",
		"Description": "rich_text",
		"Local ID":    "rich_text",
	}

	var requests atomic.Int32
	var body entryPageRequest

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want %q", r.Method, http.MethodPost)
		}

		if r.URL.Path != "/pages" {
			t.Errorf("path = %q, want %q", r.URL.Path, "/pages")
		}

		for name, want := range wantHeaders {
			if got := r.Header.Get(name); got != want {
				t.Errorf("header %s = %q, want %q", name, got, want)
			}
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body is not valid JSON: %v", err)
		}

		if _, err := w.Write([]byte(`{"object": "page", "id": "entry-page-1"}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, token, s.Client())

	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	end := time.Date(2026, 11, 10, 9, 30, 0, 0, time.UTC)
	if _, err := nc.CreateEntryPage(t.Context(), dataSourceID, title, description, localID, taskPageID, start, end); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requests.Load(); got != 1 {
		t.Errorf("unexpected request count. Got %d, want 1", got)
	}

	if body.Parent.Type != "data_source_id" || body.Parent.DataSourceID != dataSourceID {
		t.Errorf("parent = %+v, want data_source_id %q", body.Parent, dataSourceID)
	}

	props := body.Properties
	gotPropertyTypes := map[string]string{}
	for name, p := range props {
		gotPropertyTypes[name] = p.Type
	}
	if !reflect.DeepEqual(gotPropertyTypes, wantPropertyTypes) {
		t.Errorf("property types = %v, want %v", gotPropertyTypes, wantPropertyTypes)
	}

	if got := props["Title"].Title; len(got) != 1 || got[0].Text.Content != title {
		t.Errorf("Title = %+v, want %q", got, title)
	}

	if got := props["Description"].RichText; len(got) != 1 || got[0].Text.Content != description {
		t.Errorf("Description = %+v, want %q", got, description)
	}

	// Local ID is the idempotency key (DESIGN.md §6.2), so it has to arrive exactly as stored
	if got := props["Local ID"].RichText; len(got) != 1 || got[0].Text.Content != localID {
		t.Errorf("Local ID = %+v, want %q", got, localID)
	}

	if got := props["Task"].Relation; len(got) != 1 || got[0].ID != taskPageID {
		t.Errorf("Task relation = %+v, want one page %q", got, taskPageID)
	}
}

// Start and End are UTC instants truncated to the minute, never sent with time_zone
// (DESIGN.md §5 Minutes, §6.1, §10 Tests that must exist)
func TestCreateEntryPage_StartAndEnd(t *testing.T) {
	plusOne := time.FixedZone("UTC+1", 60*60)

	tests := []struct {
		name      string
		start     time.Time
		end       time.Time
		wantStart string
		wantEnd   string
	}{
		{
			name:      "whole minutes are unchanged",
			start:     time.Date(2026, 11, 10, 9, 1, 0, 0, time.UTC),
			end:       time.Date(2026, 11, 10, 9, 31, 0, 0, time.UTC),
			wantStart: "2026-11-10T09:01:00Z",
			wantEnd:   "2026-11-10T09:31:00Z",
		},
		{
			name:      "seconds are truncated, not rounded",
			start:     time.Date(2026, 11, 10, 9, 1, 50, 0, time.UTC),
			end:       time.Date(2026, 11, 10, 9, 2, 10, 0, time.UTC),
			wantStart: "2026-11-10T09:01:00Z",
			wantEnd:   "2026-11-10T09:02:00Z",
		},
		// Edge cases
		{
			name:      "fractional seconds are dropped",
			start:     time.Date(2026, 11, 10, 9, 1, 59, 999999999, time.UTC),
			end:       time.Date(2026, 11, 10, 9, 2, 0, 123456789, time.UTC),
			wantStart: "2026-11-10T09:01:00Z",
			wantEnd:   "2026-11-10T09:02:00Z",
		},
		{
			name:      "another zone is sent as UTC",
			start:     time.Date(2026, 11, 10, 10, 1, 30, 0, plusOne), // 09:01:30 UTC
			end:       time.Date(2026, 11, 10, 10, 31, 30, 0, plusOne),
			wantStart: "2026-11-10T09:01:00Z",
			wantEnd:   "2026-11-10T09:31:00Z",
		},
		{
			name:      "span across midnight UTC",
			start:     time.Date(2026, 11, 10, 23, 59, 30, 0, time.UTC),
			end:       time.Date(2026, 11, 11, 0, 0, 30, 0, time.UTC),
			wantStart: "2026-11-10T23:59:00Z",
			wantEnd:   "2026-11-11T00:00:00Z",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body entryPageRequest
			s := newEntryPageServer(t, &body)

			nc := NewClient(s.URL, "test-token", s.Client())

			if _, err := nc.CreateEntryPage(t.Context(), "entries-ds", "title", "description", "local-1", "task-page", tc.start, tc.end); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for name, want := range map[string]string{"Start": tc.wantStart, "End": tc.wantEnd} {
				date := body.Properties[name].Date
				if date == nil {
					t.Errorf("%s has no date", name)
					continue
				}

				if date.Start != want {
					t.Errorf("%s = %q, want %q", name, date.Start, want)
				}

				// Notion keeps the wall-clock digits and swaps in the zone's offset, which changes the instant
				if date.TimeZone != nil {
					t.Errorf("%s time_zone = %q, want the key to be omitted", name, *date.TimeZone)
				}
			}
		})
	}
}

// The title and description are typed by the user, so any text they can type has to reach Notion
// unchanged and still leave the request valid JSON
func TestCreateEntryPage_TextIsSentVerbatim(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{name: "plain text", text: "Writing tests"},
		// Edge cases
		{name: "double quotes", text: `Reviewing "push.go"`},
		{name: "backslash", text: `C:\Users\tt`},
		{name: "newline", text: "First line\nSecond line"},
		{name: "tab", text: "Before\tafter"},
		{name: "non-ASCII", text: "Café ☕ 日本語"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body entryPageRequest
			s := newEntryPageServer(t, &body)

			nc := NewClient(s.URL, "test-token", s.Client())

			start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
			if _, err := nc.CreateEntryPage(t.Context(), "entries-ds", tc.text, tc.text, "local-1", "task-page", start, start.Add(time.Hour)); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := body.Properties["Title"].Title; len(got) != 1 || got[0].Text.Content != tc.text {
				t.Errorf("Title = %+v, want %q", got, tc.text)
			}

			if got := body.Properties["Description"].RichText; len(got) != 1 || got[0].Text.Content != tc.text {
				t.Errorf("Description = %+v, want %q", got, tc.text)
			}
		})
	}
}

func TestCreateEntryPage_ErrorResponse_ReturnsCLIError(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode clierr.ErrorCode
	}{
		{
			name:     "bad request",
			status:   http.StatusBadRequest,
			body:     `{"object": "error", "status": 400, "code": "validation_error", "message": "body failed validation"}`,
			wantCode: clierr.Usage,
		},
		{
			name:     "invalid token",
			status:   http.StatusUnauthorized,
			body:     `{"object": "error", "status": 401, "code": "unauthorized", "message": "API token is invalid."}`,
			wantCode: clierr.AccessDenied,
		},
		{
			// What Notion returns for a data source that isn't shared with the integration, too
			name:     "data source not found",
			status:   http.StatusNotFound,
			body:     `{"object": "error", "status": 404, "code": "object_not_found", "message": "Could not find data source."}`,
			wantCode: clierr.NotFound,
		},
		{
			name:     "server error",
			status:   http.StatusInternalServerError,
			body:     `{"object": "error", "status": 500, "code": "internal_server_error", "message": "Unexpected error occurred."}`,
			wantCode: clierr.Internal,
		},
		// Edge cases
		{
			name:     "unmapped status",
			status:   http.StatusServiceUnavailable,
			body:     `{"object": "error", "status": 503, "code": "service_unavailable", "message": "Notion is unavailable."}`,
			wantCode: clierr.Internal,
		},
		{
			name:     "error body is not JSON",
			status:   http.StatusBadGateway,
			body:     `<html><body>502 Bad Gateway</body></html>`,
			wantCode: clierr.Internal,
		},
		{
			// A 200 that can't be read must not look like a created page
			name:     "success body is not JSON",
			status:   http.StatusOK,
			body:     `<html><body>not JSON</body></html>`,
			wantCode: clierr.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("writing response: %v", err)
				}
			}))
			t.Cleanup(s.Close)

			nc := NewClient(s.URL, "test-token", s.Client())

			start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
			got, err := nc.CreateEntryPage(t.Context(), "entries-ds", "title", "description", "local-1", "task-page", start, start.Add(time.Hour))
			if err == nil {
				t.Fatalf("expected an error, got page %q", got.ID)
			}

			if got.ID != "" {
				t.Errorf("expected no page alongside an error, got %q", got.ID)
			}

			var ce *clierr.Error
			if !errors.As(err, &ce) {
				t.Fatalf("error %v is not a *clierr.Error", err)
			}

			if ce.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q", ce.Code, tc.wantCode)
			}
		})
	}
}

// When Notion can't be reached there's no HTTP status to map, so the error is offline (DESIGN.md §8)
func TestCreateEntryPage_Unreachable_ReturnsOffline(t *testing.T) {
	const token = "secret-test-token"

	// See TestQueryDataSource_Unreachable_ReturnsOffline for why this needs NewServer
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to a closed server: %s %s", r.Method, r.URL.Path)
	}))
	nc := NewClient(s.URL, token, s.Client())

	s.Close()

	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	got, err := nc.CreateEntryPage(t.Context(), "entries-ds", "title", "description", "local-1", "task-page", start, start.Add(time.Hour))
	if err == nil {
		t.Fatalf("expected an error, got page %q", got.ID)
	}

	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}

	if ce.Code != clierr.Offline {
		t.Errorf("error code = %q, want %q", ce.Code, clierr.Offline)
	}

	// The cause is printed with the error, so it must never carry the token (DESIGN.md §9)
	if strings.Contains(err.Error(), token) || (ce.Cause != nil && strings.Contains(ce.Cause.Error(), token)) {
		t.Errorf("error includes the token: %v", err)
	}
}

func TestFindEntryPage_Found_ReturnsPage(t *testing.T) {
	const want = "entry-page-1"

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responseBody := `{
			"object": "list",
			"type": "page_or_data_source",
			"has_more": false,
			"next_cursor": null,
			"results": [{"object": "page", "id": "entry-page-1"}]
		}`

		if _, err := w.Write([]byte(responseBody)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, "test-token", s.Client())

	page, found, err := nc.FindEntryPage(t.Context(), "entries-ds", "Local ID", "local-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !found {
		t.Fatal("found = false, want true")
	}

	if page.ID != want {
		t.Errorf("page ID = %q, want %q", page.ID, want)
	}
}

// No match is the usual case on a first sync, so it's not an error: the caller goes on to create
func TestFindEntryPage_NotFound(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "empty results", body: `{"object": "list", "has_more": false, "next_cursor": null, "results": []}`},
		// Edge cases
		{name: "null results", body: `{"object": "list", "has_more": false, "next_cursor": null, "results": null}`},
		{name: "missing results", body: `{"object": "list", "has_more": false, "next_cursor": null}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("writing response: %v", err)
				}
			}))
			t.Cleanup(s.Close)

			nc := NewClient(s.URL, "test-token", s.Client())

			page, found, err := nc.FindEntryPage(t.Context(), "entries-ds", "Local ID", "local-1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if found {
				t.Error("found = true, want false")
			}

			if page.ID != "" {
				t.Errorf("expected no page when nothing is found, got %q", page.ID)
			}
		})
	}
}

// The lookup is the idempotency check (DESIGN.md §6.2), so it has to filter on exactly the Local ID
// property and value, or a retry could miss the row it created and duplicate it
func TestFindEntryPage_RequestShape(t *testing.T) {
	const (
		dataSourceID = "entries-ds"
		localIDKey   = "Local ID"
		localID      = "0b9e6c3a-1f2d-4e5a-8b7c-6d5e4f3a2b1c"
		token        = "test-token"
	)

	wantPath := "/data_sources/" + dataSourceID + "/query"
	wantHeaders := map[string]string{
		"Authorization":  "Bearer " + token,
		"Notion-Version": "2026-03-11",
		"Content-Type":   "application/json",
	}

	var requests atomic.Int32
	// Decoded into a map rather than a struct, so a missing key can be told apart from an empty value
	var body map[string]any

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		if r.Method != http.MethodPost {
			t.Errorf("method = %q, want %q", r.Method, http.MethodPost)
		}

		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}

		for name, want := range wantHeaders {
			if got := r.Header.Get(name); got != want {
				t.Errorf("header %s = %q, want %q", name, got, want)
			}
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body is not valid JSON: %v", err)
		}

		if _, err := w.Write([]byte(`{"object": "list", "has_more": false, "results": []}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, token, s.Client())

	if _, _, err := nc.FindEntryPage(t.Context(), dataSourceID, localIDKey, localID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requests.Load(); got != 1 {
		t.Errorf("unexpected request count. Got %d, want 1", got)
	}

	// A lookup never pages, so there's no cursor to send
	if cursor, ok := body["start_cursor"]; ok {
		t.Errorf("start_cursor = %v, want the key to be omitted", cursor)
	}

	// encoding/json decodes JSON numbers into float64 when the target is any
	if got, want := body["page_size"], float64(1); got != want {
		t.Errorf("page_size = %v, want %v", got, want)
	}

	filter, ok := body["filter"].(map[string]any)
	if !ok {
		t.Fatalf("filter = %v, want an object", body["filter"])
	}

	if got := filter["property"]; got != localIDKey {
		t.Errorf("filter property = %v, want %q", got, localIDKey)
	}

	richText, ok := filter["rich_text"].(map[string]any)
	if !ok {
		t.Fatalf("filter rich_text = %v, want an object", filter["rich_text"])
	}

	if got := richText["equals"]; got != localID {
		t.Errorf("filter rich_text.equals = %v, want %q", got, localID)
	}
}

func TestFindEntryPage_ErrorResponse_ReturnsCLIError(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode clierr.ErrorCode
	}{
		{
			name:     "bad request",
			status:   http.StatusBadRequest,
			body:     `{"object": "error", "status": 400, "code": "validation_error", "message": "body failed validation"}`,
			wantCode: clierr.Usage,
		},
		{
			name:     "invalid token",
			status:   http.StatusUnauthorized,
			body:     `{"object": "error", "status": 401, "code": "unauthorized", "message": "API token is invalid."}`,
			wantCode: clierr.AccessDenied,
		},
		{
			// What Notion returns for a data source that isn't shared with the integration, too
			name:     "data source not found",
			status:   http.StatusNotFound,
			body:     `{"object": "error", "status": 404, "code": "object_not_found", "message": "Could not find data source."}`,
			wantCode: clierr.NotFound,
		},
		{
			name:     "server error",
			status:   http.StatusInternalServerError,
			body:     `{"object": "error", "status": 500, "code": "internal_server_error", "message": "Unexpected error occurred."}`,
			wantCode: clierr.Internal,
		},
		// Edge cases
		{
			name:     "unmapped status",
			status:   http.StatusServiceUnavailable,
			body:     `{"object": "error", "status": 503, "code": "service_unavailable", "message": "Notion is unavailable."}`,
			wantCode: clierr.Internal,
		},
		{
			name:     "error body is not JSON",
			status:   http.StatusBadGateway,
			body:     `<html><body>502 Bad Gateway</body></html>`,
			wantCode: clierr.Internal,
		},
		{
			// A 200 that can't be read must not look like "not found", or sync would create a duplicate
			name:     "success body is not JSON",
			status:   http.StatusOK,
			body:     `<html><body>not JSON</body></html>`,
			wantCode: clierr.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("writing response: %v", err)
				}
			}))
			t.Cleanup(s.Close)

			nc := NewClient(s.URL, "test-token", s.Client())

			page, found, err := nc.FindEntryPage(t.Context(), "entries-ds", "Local ID", "local-1")
			if err == nil {
				t.Fatalf("expected an error, got found = %v, page %q", found, page.ID)
			}

			if found || page.ID != "" {
				t.Errorf("expected no page alongside an error, got found = %v, page %q", found, page.ID)
			}

			var ce *clierr.Error
			if !errors.As(err, &ce) {
				t.Fatalf("error %v is not a *clierr.Error", err)
			}

			if ce.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q", ce.Code, tc.wantCode)
			}
		})
	}
}

// When Notion can't be reached there's no HTTP status to map, so the error is offline (DESIGN.md §8)
func TestFindEntryPage_Unreachable_ReturnsOffline(t *testing.T) {
	const token = "secret-test-token"

	// See TestQueryDataSource_Unreachable_ReturnsOffline for why this needs NewServer
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to a closed server: %s %s", r.Method, r.URL.Path)
	}))
	nc := NewClient(s.URL, token, s.Client())

	s.Close()

	page, found, err := nc.FindEntryPage(t.Context(), "entries-ds", "Local ID", "local-1")
	if err == nil {
		t.Fatalf("expected an error, got found = %v, page %q", found, page.ID)
	}

	// Offline must not read as "not found", or the caller would go on to create
	if found {
		t.Error("found = true alongside an error, want false")
	}

	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}

	if ce.Code != clierr.Offline {
		t.Errorf("error code = %q, want %q", ce.Code, clierr.Offline)
	}

	// The cause is printed with the error, so it must never carry the token (DESIGN.md §9)
	if strings.Contains(err.Error(), token) || (ce.Cause != nil && strings.Contains(ce.Cause.Error(), token)) {
		t.Errorf("error includes the token: %v", err)
	}
}

func TestUpdateEntryPage_ReturnsPage(t *testing.T) {
	const want = "entry-page-1"

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responseBody := `{
			"object": "page",
			"id": "entry-page-1",
			"created_time": "2026-11-10T09:30:00.000Z",
			"last_edited_time": "2026-11-10T10:15:00.000Z",
			"in_trash": false,
			"properties": {
				"Title": {"id": "title", "type": "title", "title": [{"plain_text": "Writing more tests"}]}
			}
		}`

		if _, err := w.Write([]byte(responseBody)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, "test-token", s.Client())

	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	got, err := nc.UpdateEntryPage(t.Context(), want, "entries-ds", "Writing more tests", "Writing more tests", "local-1", "task-page", start, start.Add(45*time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.ID != want {
		t.Errorf("page ID = %q, want %q", got.ID, want)
	}
}

// An update sends the same properties as a create (DESIGN.md §5 step 6), but as a PATCH to the page
// itself, so a retry rewrites the row in place rather than adding one (§6.2)
func TestUpdateEntryPage_RequestShape(t *testing.T) {
	const (
		pageID       = "59833787-2cf9-4fdf-8782-e53db20768a5"
		dataSourceID = "entries-ds"
		title        = "Writing tests"
		description  = "Tests for UpdateEntryPage"
		localID      = "0b9e6c3a-1f2d-4e5a-8b7c-6d5e4f3a2b1c"
		taskPageID   = "task-page"
		token        = "test-token"
	)

	wantPath := "/pages/" + pageID
	wantHeaders := map[string]string{
		"Authorization":  "Bearer " + token,
		"Notion-Version": "2026-03-11",
		"Content-Type":   "application/json",
	}
	// Minutes is a formula, so Notion works it out and it is never sent
	wantPropertyTypes := map[string]string{
		"Title":       "title",
		"Task":        "relation",
		"Start":       "date",
		"End":         "date",
		"Description": "rich_text",
		"Local ID":    "rich_text",
	}

	var requests atomic.Int32
	var body entryPageRequest

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		if r.Method != http.MethodPatch {
			t.Errorf("method = %q, want %q", r.Method, http.MethodPatch)
		}

		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}

		for name, want := range wantHeaders {
			if got := r.Header.Get(name); got != want {
				t.Errorf("header %s = %q, want %q", name, got, want)
			}
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request body is not valid JSON: %v", err)
		}

		if _, err := w.Write([]byte(`{"object": "page", "id": "` + pageID + `"}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, token, s.Client())

	// Seconds and a non-UTC zone, so the test also shows the shared truncation reaches an update
	plusOne := time.FixedZone("UTC+1", 60*60)
	start := time.Date(2026, 11, 10, 10, 1, 50, 0, plusOne) // 09:01:50 UTC
	end := time.Date(2026, 11, 10, 10, 31, 10, 0, plusOne)  // 09:31:10 UTC
	if _, err := nc.UpdateEntryPage(t.Context(), pageID, dataSourceID, title, description, localID, taskPageID, start, end); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requests.Load(); got != 1 {
		t.Errorf("unexpected request count. Got %d, want 1", got)
	}

	props := body.Properties
	gotPropertyTypes := map[string]string{}
	for name, p := range props {
		gotPropertyTypes[name] = p.Type
	}
	if !reflect.DeepEqual(gotPropertyTypes, wantPropertyTypes) {
		t.Errorf("property types = %v, want %v", gotPropertyTypes, wantPropertyTypes)
	}

	if got := props["Title"].Title; len(got) != 1 || got[0].Text.Content != title {
		t.Errorf("Title = %+v, want %q", got, title)
	}

	if got := props["Description"].RichText; len(got) != 1 || got[0].Text.Content != description {
		t.Errorf("Description = %+v, want %q", got, description)
	}

	// Local ID is the idempotency key (DESIGN.md §6.2), so an update must not change or drop it
	if got := props["Local ID"].RichText; len(got) != 1 || got[0].Text.Content != localID {
		t.Errorf("Local ID = %+v, want %q", got, localID)
	}

	// Re-attaching an entry (tt attach, tt edit --task) reaches Notion through this relation
	if got := props["Task"].Relation; len(got) != 1 || got[0].ID != taskPageID {
		t.Errorf("Task relation = %+v, want one page %q", got, taskPageID)
	}

	for name, want := range map[string]string{"Start": "2026-11-10T09:01:00Z", "End": "2026-11-10T09:31:00Z"} {
		date := props[name].Date
		if date == nil {
			t.Errorf("%s has no date", name)
			continue
		}

		if date.Start != want {
			t.Errorf("%s = %q, want %q", name, date.Start, want)
		}

		// See TestCreateEntryPage_StartAndEnd
		if date.TimeZone != nil {
			t.Errorf("%s time_zone = %q, want the key to be omitted", name, *date.TimeZone)
		}
	}
}

func TestUpdateEntryPage_ErrorResponse_ReturnsCLIError(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode clierr.ErrorCode
	}{
		{
			name:     "bad request",
			status:   http.StatusBadRequest,
			body:     `{"object": "error", "status": 400, "code": "validation_error", "message": "body failed validation"}`,
			wantCode: clierr.Usage,
		},
		{
			// Notion answers an update to a trashed page with a 400, not a 404 (DESIGN.md §6.3). Telling
			// row_missing from bad_request is the sync's job, so the client just reports the 400
			name:     "page is trashed",
			status:   http.StatusBadRequest,
			body:     `{"object": "error", "status": 400, "code": "validation_error", "message": "Can't edit block that is archived. You must unarchive the block before editing."}`,
			wantCode: clierr.Usage,
		},
		{
			name:     "invalid token",
			status:   http.StatusUnauthorized,
			body:     `{"object": "error", "status": 401, "code": "unauthorized", "message": "API token is invalid."}`,
			wantCode: clierr.AccessDenied,
		},
		{
			// What Notion returns for a page that isn't shared with the integration, too
			name:     "page not found",
			status:   http.StatusNotFound,
			body:     `{"object": "error", "status": 404, "code": "object_not_found", "message": "Could not find page."}`,
			wantCode: clierr.NotFound,
		},
		{
			name:     "server error",
			status:   http.StatusInternalServerError,
			body:     `{"object": "error", "status": 500, "code": "internal_server_error", "message": "Unexpected error occurred."}`,
			wantCode: clierr.Internal,
		},
		// Edge cases
		{
			name:     "unmapped status",
			status:   http.StatusServiceUnavailable,
			body:     `{"object": "error", "status": 503, "code": "service_unavailable", "message": "Notion is unavailable."}`,
			wantCode: clierr.Internal,
		},
		{
			name:     "error body is not JSON",
			status:   http.StatusBadGateway,
			body:     `<html><body>502 Bad Gateway</body></html>`,
			wantCode: clierr.Internal,
		},
		{
			// A 200 that can't be read must not look like an updated page
			name:     "success body is not JSON",
			status:   http.StatusOK,
			body:     `<html><body>not JSON</body></html>`,
			wantCode: clierr.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Errorf("writing response: %v", err)
				}
			}))
			t.Cleanup(s.Close)

			nc := NewClient(s.URL, "test-token", s.Client())

			start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
			got, err := nc.UpdateEntryPage(t.Context(), "entry-page-1", "entries-ds", "title", "description", "local-1", "task-page", start, start.Add(time.Hour))
			if err == nil {
				t.Fatalf("expected an error, got page %q", got.ID)
			}

			if got.ID != "" {
				t.Errorf("expected no page alongside an error, got %q", got.ID)
			}

			var ce *clierr.Error
			if !errors.As(err, &ce) {
				t.Fatalf("error %v is not a *clierr.Error", err)
			}

			if ce.Code != tc.wantCode {
				t.Errorf("error code = %q, want %q", ce.Code, tc.wantCode)
			}
		})
	}
}

// When Notion can't be reached there's no HTTP status to map, so the error is offline (DESIGN.md §8),
// which keeps the entry pending rather than failed (§6.3)
func TestUpdateEntryPage_Unreachable_ReturnsOffline(t *testing.T) {
	const token = "secret-test-token"

	// See TestQueryDataSource_Unreachable_ReturnsOffline for why this needs NewServer
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to a closed server: %s %s", r.Method, r.URL.Path)
	}))
	nc := NewClient(s.URL, token, s.Client())

	s.Close()

	start := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	got, err := nc.UpdateEntryPage(t.Context(), "entry-page-1", "entries-ds", "title", "description", "local-1", "task-page", start, start.Add(time.Hour))
	if err == nil {
		t.Fatalf("expected an error, got page %q", got.ID)
	}

	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}

	if ce.Code != clierr.Offline {
		t.Errorf("error code = %q, want %q", ce.Code, clierr.Offline)
	}

	// The cause is printed with the error, so it must never carry the token (DESIGN.md §9)
	if strings.Contains(err.Error(), token) || (ce.Cause != nil && strings.Contains(ce.Cause.Error(), token)) {
		t.Errorf("error includes the token: %v", err)
	}
}
