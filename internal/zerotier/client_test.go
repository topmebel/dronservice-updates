package zerotier

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func testTokenFile(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("test-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestSnapshotUsesLocalAPIAndDoesNotExposeToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-ZT1-Auth") != "test-secret" {
			t.Fatalf("authentication header = %q", r.Header.Get("X-ZT1-Auth"))
		}
		switch r.URL.Path {
		case "/status":
			_, _ = w.Write([]byte(`{"address":"293e8a573d","online":true,"version":"1.16.2"}`))
		case "/network":
			_, _ = w.Write([]byte(`[{"nwid":"ec37785c64ca5859","name":"camera-net","status":"OK","assignedAddresses":["10.1.2.3/24"]}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, testTokenFile(t))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := client.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Status.Online || snapshot.Status.Address != "293e8a573d" || len(snapshot.Networks) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.Networks[0].ID != "ec37785c64ca5859" {
		t.Fatalf("network ID = %q", snapshot.Networks[0].ID)
	}
}

func TestJoinAndLeave(t *testing.T) {
	requests := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(server.URL, testTokenFile(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Join(context.Background(), "ec37785c64ca5859"); err != nil {
		t.Fatal(err)
	}
	if err := client.Leave(context.Background(), "ec37785c64ca5859"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0] != "POST /network/ec37785c64ca5859" || requests[1] != "DELETE /network/ec37785c64ca5859" {
		t.Fatalf("requests = %#v", requests)
	}
}
