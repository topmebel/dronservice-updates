package updater

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusFindsAndCachesNewGitHubRelease(t *testing.T) {
	directory := t.TempDir()
	client, err := NewClient(Config{
		Repository:     "owner/repository",
		CurrentVersion: "v0.1.0",
		RequestPath:    filepath.Join(directory, "request"),
		StatusPath:     filepath.Join(directory, "status.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	requests := 0
	client.http = &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Path != "/repos/owner/repository/releases/latest" {
			t.Fatalf("request path = %q", request.URL.Path)
		}
		return releaseResponse(`{
			"tag_name":"v0.2.0",
			"html_url":"https://github.com/owner/repository/releases/tag/v0.2.0",
			"body":"Release notes",
			"assets":[
				{"name":"dronservice-linux-arm64"},
				{"name":"checksums.sha256"},
				{"name":"dronservice-linux-arm64.sig"}
			]
		}`), nil
	})}
	client.apiBase = "https://github.test"

	for range 2 {
		status := client.Status(context.Background())
		if !status.Enabled || !status.UpdateAvailable || status.LatestVersion != "v0.2.0" || status.ReleaseNotes != "Release notes" {
			t.Fatalf("Status() = %+v", status)
		}
	}
	if requests != 1 {
		t.Fatalf("GitHub requests = %d, want 1", requests)
	}
}

func TestRequestWritesOnlyValidatedVersion(t *testing.T) {
	directory := t.TempDir()
	requestPath := filepath.Join(directory, "request")
	client, err := NewClient(Config{Repository: "owner/repository", CurrentVersion: "v0.1.0", RequestPath: requestPath, StatusPath: filepath.Join(directory, "status.json")})
	if err != nil {
		t.Fatal(err)
	}
	client.http = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return releaseResponse(`{"tag_name":"v0.2.0","assets":[{"name":"dronservice-linux-arm64"},{"name":"checksums.sha256"},{"name":"dronservice-linux-arm64.sig"}]}`), nil
	})}

	if err := client.Request(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v0.2.0\n" {
		t.Fatalf("request = %q", data)
	}
	if status := client.Status(context.Background()); status.State != StatePending || !status.Installing {
		t.Fatalf("status after request = %+v", status)
	}
	if matches, err := filepath.Glob(filepath.Join(directory, ".update-dronservice-request-*")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary request files = %v, err = %v", matches, err)
	}
}

func TestRequestDoesNotReplacePendingRequest(t *testing.T) {
	directory := t.TempDir()
	requestPath := filepath.Join(directory, "request")
	if err := os.WriteFile(requestPath, []byte("v0.1.9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(Config{Repository: "owner/repository", CurrentVersion: "v0.1.0", RequestPath: requestPath})
	if err != nil {
		t.Fatal(err)
	}
	client.http = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return releaseResponse(`{"tag_name":"v0.2.0","assets":[{"name":"dronservice-linux-arm64"},{"name":"checksums.sha256"},{"name":"dronservice-linux-arm64.sig"}]}`), nil
	})}

	if err := client.Request(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(requestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v0.1.9\n" {
		t.Fatalf("pending request was replaced with %q", data)
	}
}

func TestStatusDoesNotOfferDowngrade(t *testing.T) {
	client, err := NewClient(Config{Repository: "owner/repository", CurrentVersion: "v0.3.0", RequestPath: filepath.Join(t.TempDir(), "request")})
	if err != nil {
		t.Fatal(err)
	}
	client.http = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return releaseResponse(`{"tag_name":"v0.2.0","assets":[{"name":"dronservice-linux-arm64"},{"name":"checksums.sha256"},{"name":"dronservice-linux-arm64.sig"}]}`), nil
	})}
	if status := client.Status(context.Background()); status.UpdateAvailable {
		t.Fatalf("downgrade was offered: %+v", status)
	}
}

func TestStatusReportsDisabledReasonWithoutRepository(t *testing.T) {
	client, err := NewClient(Config{CurrentVersion: "v0.1.0", RequestPath: filepath.Join(t.TempDir(), "request")})
	if err != nil {
		t.Fatal(err)
	}
	status := client.Status(context.Background())
	if status.Enabled || status.DisabledReason != DisabledReasonRepositoryNotConfigured {
		t.Fatalf("Status() = %+v", status)
	}
}

func TestNewClientRejectsInvalidRepository(t *testing.T) {
	if _, err := NewClient(Config{Repository: "owner/repo;reboot", CurrentVersion: "v0.1.0"}); err == nil {
		t.Fatal("NewClient() error = nil")
	}
}

func releaseResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
