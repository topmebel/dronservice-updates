package mediamtx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientListPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/paths/list" {
			t.Fatalf("path = %q, want /v3/paths/list", r.URL.Path)
		}
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			t.Fatal("expected configured basic authentication")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"camera1","online":false,"readers":[{}],"inboundBytes":12,"outboundBytes":34}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "admin", "secret")
	paths, err := client.ListPaths(context.Background())
	if err != nil {
		t.Fatalf("ListPaths() error = %v", err)
	}
	if len(paths) != 1 || paths[0].Name != "camera1" {
		t.Fatalf("ListPaths() = %#v", paths)
	}
	if got := len(paths[0].Readers); got != 1 {
		t.Fatalf("readers = %d, want 1", got)
	}
}

func TestClientReturnsTypedHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	err := NewClient(server.URL, "", "").DeleteConfigPath(context.Background(), "missing")
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("DeleteConfigPath() error = %T %v, want *HTTPError", err, err)
	}
	if httpErr.StatusCode != http.StatusNotFound || !IsHTTPStatus(err, http.StatusNotFound) || IsHTTPStatus(err, http.StatusUnauthorized) {
		t.Fatalf("HTTP error = %+v", httpErr)
	}
}

func TestClientListPathsErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
	}{
		{name: "non-success status", code: http.StatusUnauthorized, body: `unauthorized`},
		{name: "invalid JSON", code: http.StatusOK, body: `{`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.code)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			_, err := NewClient(server.URL, "", "").ListPaths(context.Background())
			if err == nil {
				t.Fatal("ListPaths() error = nil")
			}
		})
	}
}
