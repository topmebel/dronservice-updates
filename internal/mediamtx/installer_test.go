package mediamtx

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerStatusCachesLatestRelease(t *testing.T) {
	directory := t.TempDir()
	installer := NewInstaller(filepath.Join(directory, "mediamtx"), filepath.Join(directory, "install.request"))
	requests := 0
	installer.http = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.20.0"}`))}, nil
	})}

	for range 2 {
		status, err := installer.Status(context.Background())
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if status.LatestVersion != "1.20.0" {
			t.Fatalf("LatestVersion = %q, want 1.20.0", status.LatestVersion)
		}
	}
	if requests != 1 {
		t.Fatalf("release requests = %d, want 1", requests)
	}
}

func TestInstallerStatusCachesReleaseLookupFailure(t *testing.T) {
	directory := t.TempDir()
	installer := NewInstaller(filepath.Join(directory, "mediamtx"), filepath.Join(directory, "install.request"))
	requests := 0
	installer.http = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return nil, errors.New("network unavailable")
	})}

	for range 2 {
		if _, err := installer.Status(context.Background()); err != nil {
			t.Fatalf("Status: %v", err)
		}
	}
	if requests != 1 {
		t.Fatalf("release requests = %d, want 1", requests)
	}
}

func TestInstallerRequestsInstallationOnce(t *testing.T) {
	directory := t.TempDir()
	installer := NewInstaller(filepath.Join(directory, "mediamtx"), filepath.Join(directory, "install.request"))
	installer.http = testReleaseClient(t)
	if err := installer.Request(context.Background()); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := installer.Request(context.Background()); err != nil {
		t.Fatalf("second Request: %v", err)
	}
	status, err := installer.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Installed || !status.Installing {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestInstallerRequestsUpdateWhenInstalled(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "mediamtx")
	request := filepath.Join(directory, "install.request")
	if err := os.WriteFile(binary, nil, 0700); err != nil {
		t.Fatal(err)
	}
	installer := NewInstaller(binary, request)
	installer.http = testReleaseClient(t)
	if err := installer.Request(context.Background()); err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := os.Stat(request); err != nil {
		t.Fatalf("request file missing: %v", err)
	}
}

func testReleaseClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.20.0"}`))}, nil
	})}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
