package notion

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aims519/timetrack-notion/internal/clierr"
)

func TestWhoAmI_ReturnsName(t *testing.T) {
	const want = "My integration"

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The workspace has a name too, and it isn't the one wanted
		responseBody := `{
			"object": "user",
			"id": "bot-1",
			"name": "My integration",
			"type": "bot",
			"bot": {
				"owner": {"type": "workspace", "workspace": true},
				"workspace_name": "My workspace"
			}
		}`

		if _, err := w.Write([]byte(responseBody)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, "test-token", s.Client())

	got, err := nc.WhoAmI(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWhoAmI_RequestShape(t *testing.T) {
	const token = "test-token"

	// Values come from DESIGN.md (§5 and Constraints) and the Notion API, not from
	// the client's own constants, so a change to either is caught here.
	const wantPath = "/users/me"
	wantHeaders := map[string]string{
		"Authorization":  "Bearer " + token,
		"Notion-Version": "2026-03-11",
	}

	var requests atomic.Int32

	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)

		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want %q", r.Method, http.MethodGet)
		}

		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}

		for name, want := range wantHeaders {
			if got := r.Header.Get(name); got != want {
				t.Errorf("header %s = %q, want %q", name, got, want)
			}
		}

		if _, err := w.Write([]byte(`{"object": "user", "name": "My integration"}`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, token, s.Client())

	if _, err := nc.WhoAmI(t.Context()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requests.Load(); got != 1 {
		t.Errorf("unexpected request count. Got %d, want 1", got)
	}
}

func TestWhoAmI_ErrorResponse_ReturnsCLIError(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode clierr.ErrorCode
	}{
		{
			name:     "invalid token",
			status:   http.StatusUnauthorized,
			body:     `{"object": "error", "status": 401, "code": "unauthorized", "message": "API token is invalid."}`,
			wantCode: clierr.AccessDenied,
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

			got, err := nc.WhoAmI(t.Context())
			if err == nil {
				t.Fatalf("expected an error for HTTP %d, got nil", tc.status)
			}

			if got != "" {
				t.Errorf("expected no name alongside an error, got %q", got)
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

func TestWhoAmI_InvalidJSON_ReturnsInternal(t *testing.T) {
	s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(`<html><body>not JSON</body></html>`)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(s.Close)

	nc := NewClient(s.URL, "test-token", s.Client())

	got, err := nc.WhoAmI(t.Context())
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if got != "" {
		t.Errorf("expected no name alongside an error, got %q", got)
	}

	var ce *clierr.Error
	if !errors.As(err, &ce) {
		t.Fatalf("error %v is not a *clierr.Error", err)
	}

	if ce.Code != clierr.Internal {
		t.Errorf("error code = %q, want %q", ce.Code, clierr.Internal)
	}
}

// When Notion can't be reached there's no HTTP status to map, so the error is offline (DESIGN.md §8)
func TestWhoAmI_Unreachable_ReturnsOffline(t *testing.T) {
	const token = "secret-test-token"

	tests := []struct {
		name      string
		newClient func(t *testing.T) *Client
	}{
		{
			name: "server not listening",
			newClient: func(t *testing.T) *Client {
				// See TestQueryDataSource_Unreachable_ReturnsOffline for why this needs NewServer
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request to a closed server: %s %s", r.Method, r.URL.Path)
				}))
				nc := NewClient(s.URL, token, s.Client())

				s.Close()

				return nc
			},
		},
		{
			name: "request times out",
			newClient: func(t *testing.T) *Client {
				// See TestQueryDataSource_Unreachable_ReturnsOffline for the order of these cleanups
				release := make(chan struct{})
				s := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					<-release
				}))
				t.Cleanup(func() { close(release) })

				httpClient := s.Client()
				httpClient.Timeout = 50 * time.Millisecond

				return NewClient(s.URL, token, httpClient)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nc := tc.newClient(t)

			got, err := nc.WhoAmI(t.Context())
			if err == nil {
				t.Fatal("expected an error, got nil")
			}

			if got != "" {
				t.Errorf("expected no name alongside an error, got %q", got)
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
		})
	}
}
