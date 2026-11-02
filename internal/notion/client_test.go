package notion

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestQueryDataSource_SingleResultReturned(t *testing.T) {
	const expectedResultCount = 1

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		responseBody := `{
			"object": "list",
			"type": "page_or_data_source",
			"has_more": false,
			"next_cursor": "",
			"results": [{"object": "page", "id": "page-1"}]
		}`

		if _, err := w.Write([]byte(responseBody)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, "test-token", s.Client())

	pages, err := nc.QueryDataSource(t.Context(), "1234", nil)
	if err != nil {
		t.Fatalf("unexpected response: %v", err)
	}

	if len(pages) != expectedResultCount {
		t.Fatalf("unexpected result count. Got %d, want %d", len(pages), expectedResultCount)
	}

	if pages[0].ID != "page-1" {
		t.Errorf("Got id %q, want %q", pages[0].ID, "page-1")
	}
}

func TestQueryDataSource_PaginatedResultsReturned(t *testing.T) {
	const maxRequests = 5
	wantIDs := []string{"page-1", "page-2", "page-3"}

	// Each response is keyed by the start_cursor that should fetch it.
	// The first request has no cursor, so it is keyed by "".
	responses := map[string]string{
		"": `{
			"object": "list",
			"type": "page_or_data_source",
			"has_more": true,
			"next_cursor": "cursor-2",
			"results": [{"object": "page", "id": "page-1"}]
		}`,
		"cursor-2": `{
			"object": "list",
			"type": "page_or_data_source",
			"has_more": true,
			"next_cursor": "cursor-3",
			"results": [{"object": "page", "id": "page-2"}]
		}`,
		"cursor-3": `{
			"object": "list",
			"type": "page_or_data_source",
			"has_more": false,
			"next_cursor": null,
			"results": [{"object": "page", "id": "page-3"}]
		}`,
	}

	// The handler runs on the server's goroutine, so the counter is atomic.
	var requests atomic.Int32

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Guard against a client that never stops paging, which would
		// otherwise hang the test.
		if n := requests.Add(1); n > maxRequests {
			t.Errorf("client made more than %d requests; is it following next_cursor?", maxRequests)
			writeBadRequest(t, w, "too many requests from client")
			return
		}

		var body struct {
			StartCursor string `json:"start_cursor"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request body: %v", err)
			writeBadRequest(t, w, "invalid request body")
			return
		}

		res, ok := responses[body.StartCursor]
		if !ok {
			t.Errorf("unexpected start_cursor %q", body.StartCursor)
			writeBadRequest(t, w, "unknown start_cursor")
			return
		}

		if _, err := w.Write([]byte(res)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, "test-token", s.Client())

	pages, err := nc.QueryDataSource(t.Context(), "1234", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requests.Load(); got != int32(len(wantIDs)) {
		t.Errorf("unexpected request count. Got %d, want %d", got, len(wantIDs))
	}

	if len(pages) != len(wantIDs) {
		t.Fatalf("unexpected result count. Got %d, want %d", len(pages), len(wantIDs))
	}

	for i, want := range wantIDs {
		if pages[i].ID != want {
			t.Errorf("pages[%d].ID = %q, want %q", i, pages[i].ID, want)
		}
	}
}

// writeBadRequest writes a Notion-shaped 400 error response.
func writeBadRequest(t *testing.T, w http.ResponseWriter, message string) {
	t.Helper()
	w.WriteHeader(http.StatusBadRequest)
	err := json.NewEncoder(w).Encode(errorResponse{
		Object:  "error",
		Status:  http.StatusBadRequest,
		Code:    "validation_error",
		Message: message,
	})
	if err != nil {
		t.Errorf("writing error response: %v", err)
	}
}

func TestQueryDataSource_ErrorResponse_ReturnsCLIError(t *testing.T) {
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
			name:     "not found",
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
			name:     "body is not JSON",
			status:   http.StatusBadGateway,
			body:     `<html><body>502 Bad Gateway</body></html>`,
			wantCode: clierr.Internal,
		},
		{
			name:     "invalid token, body is not JSON",
			status:   http.StatusUnauthorized,
			body:     `<html><body>401 Unauthorized</body></html>`,
			wantCode: clierr.AccessDenied,
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

			pages, err := nc.QueryDataSource(t.Context(), "1234", nil)
			if err == nil {
				t.Fatalf("expected an error for HTTP %d, got nil", tc.status)
			}

			if len(pages) != 0 {
				t.Errorf("expected no pages alongside an error, got %d", len(pages))
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

func TestQueryDataSource_RequestShape(t *testing.T) {
	const (
		dataSourceID = "ds-123"
		token        = "test-token"
		maxRequests  = 5
	)

	// Values come from DESIGN.md (Constraints) and the Notion API, not from
	// the client's own constants, so a change to either is caught here.
	wantPath := "/data_sources/" + dataSourceID + "/query"
	wantHeaders := map[string]string{
		"Authorization":  "Bearer " + token,
		"Notion-Version": "2026-03-11",
		"Content-Type":   "application/json",
	}

	firstPage := `{
		"object": "list",
		"has_more": true,
		"next_cursor": "cursor-2",
		"results": [{"object": "page", "id": "page-1"}]
	}`
	lastPage := `{
		"object": "list",
		"has_more": false,
		"next_cursor": null,
		"results": [{"object": "page", "id": "page-2"}]
	}`

	var requests atomic.Int32

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n > maxRequests {
			t.Errorf("client made more than %d requests", maxRequests)
			writeBadRequest(t, w, "too many requests from client")
			return
		}

		if r.Method != http.MethodPost {
			t.Errorf("request %d: method = %q, want %q", n, r.Method, http.MethodPost)
		}

		if r.URL.Path != wantPath {
			t.Errorf("request %d: path = %q, want %q", n, r.URL.Path, wantPath)
		}

		for name, want := range wantHeaders {
			if got := r.Header.Get(name); got != want {
				t.Errorf("request %d: header %s = %q, want %q", n, name, got, want)
			}
		}

		// Decode into a map rather than a struct, so a missing key can be
		// told apart from an empty value.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("request %d: decoding body: %v", n, err)
			writeBadRequest(t, w, "invalid request body")
			return
		}

		// encoding/json decodes JSON numbers into float64 when the target is any.
		if got, want := body["page_size"], float64(queryPageSize); got != want {
			t.Errorf("request %d: page_size = %v, want %v", n, got, want)
		}

		cursor, hasCursor := body["start_cursor"]

		var res string
		switch n {
		case 1:
			if hasCursor {
				t.Errorf("request 1: start_cursor = %v, want the key to be omitted", cursor)
			}
			res = firstPage
		default:
			if cursor != "cursor-2" {
				t.Errorf("request %d: start_cursor = %v, want %q", n, cursor, "cursor-2")
			}
			res = lastPage
		}

		if _, err := w.Write([]byte(res)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, token, s.Client())

	if _, err := nc.QueryDataSource(t.Context(), dataSourceID, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requests.Load(); got != 2 {
		t.Errorf("unexpected request count. Got %d, want 2", got)
	}
}

func TestQueryDataSource_SinceFilter(t *testing.T) {
	const maxRequests = 5

	// Notion's timestamp filter on last_edited_time. The value is RFC 3339 in UTC,
	// without fractional seconds.
	sinceFilter := func(onOrAfter string) map[string]any {
		return map[string]any{
			"timestamp": "last_edited_time",
			"last_edited_time": map[string]any{
				"on_or_after": onOrAfter,
			},
		}
	}

	utc := time.Date(2026, 11, 2, 8, 58, 0, 0, time.UTC)
	plusOne := time.FixedZone("UTC+1", 60*60)
	inPlusOne := time.Date(2026, 11, 2, 9, 58, 0, 0, plusOne) // 08:58 UTC
	withNanos := time.Date(2026, 11, 2, 8, 58, 0, 123456789, time.UTC)

	tests := []struct {
		name       string
		since      *time.Time
		wantFilter map[string]any // nil means the request has no filter key
	}{
		{
			name:       "no since sends no filter",
			since:      nil,
			wantFilter: nil,
		},
		{
			name:       "since in UTC",
			since:      &utc,
			wantFilter: sinceFilter("2026-11-02T08:58:00Z"),
		},
		// Edge cases
		{
			name:       "since in another zone is sent as UTC",
			since:      &inPlusOne,
			wantFilter: sinceFilter("2026-11-02T08:58:00Z"),
		},
		{
			name:       "fractional seconds are dropped",
			since:      &withNanos,
			wantFilter: sinceFilter("2026-11-02T08:58:00Z"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Two pages, so the test also checks the filter is sent with the cursor,
			// not only on the first request
			responses := map[string]string{
				"": `{
					"object": "list",
					"has_more": true,
					"next_cursor": "cursor-2",
					"results": [{"object": "page", "id": "page-1"}]
				}`,
				"cursor-2": `{
					"object": "list",
					"has_more": false,
					"next_cursor": null,
					"results": [{"object": "page", "id": "page-2"}]
				}`,
			}

			var requests atomic.Int32

			s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				if n > maxRequests {
					t.Errorf("client made more than %d requests", maxRequests)
					writeBadRequest(t, w, "too many requests from client")
					return
				}

				// Decode into a map, so a missing filter key can be told apart from an empty one
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("request %d: decoding body: %v", n, err)
					writeBadRequest(t, w, "invalid request body")
					return
				}

				filter, hasFilter := body["filter"]
				switch {
				case tc.wantFilter == nil && hasFilter:
					t.Errorf("request %d: filter = %v, want the key to be omitted", n, filter)
				case tc.wantFilter != nil && !hasFilter:
					t.Errorf("request %d: no filter, want %v", n, tc.wantFilter)
				case tc.wantFilter != nil && !reflect.DeepEqual(filter, tc.wantFilter):
					t.Errorf("request %d: filter = %v, want %v", n, filter, tc.wantFilter)
				}

				cursor, _ := body["start_cursor"].(string)
				res, ok := responses[cursor]
				if !ok {
					t.Errorf("request %d: unexpected start_cursor %q", n, cursor)
					writeBadRequest(t, w, "unknown start_cursor")
					return
				}

				if _, err := w.Write([]byte(res)); err != nil {
					t.Errorf("writing response: %v", err)
				}
			}))
			t.Cleanup(s.Close)

			nc := NewClient(s.URL, "test-token", s.Client())

			pages, err := nc.QueryDataSource(t.Context(), "1234", tc.since)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := requests.Load(); got != 2 {
				t.Errorf("unexpected request count. Got %d, want 2", got)
			}

			if len(pages) != 2 {
				t.Errorf("unexpected result count. Got %d, want 2", len(pages))
			}
		})
	}
}
