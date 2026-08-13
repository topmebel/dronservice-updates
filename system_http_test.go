package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testAddress string

func (a testAddress) Network() string { return "ip" }
func (a testAddress) String() string  { return string(a) }

func TestInternetStatusHandlerKeepsOnlineAndAddsDetailedStatus(t *testing.T) {
	online := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer online.Close()
	previous := internetChecker
	internetChecker = newConnectivityChecker([]string{online.URL})
	t.Cleanup(func() { internetChecker = previous })

	request := httptest.NewRequest(http.MethodGet, "/api/system/internet", nil)
	response := httptest.NewRecorder()
	internetStatusHandler(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", response.Code)
	}
	var status internetStatusResponse
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !status.Online || status.Status != connectivityOnline || status.CheckedAt.IsZero() {
		t.Fatalf("response = %+v", status)
	}
}

func TestConnectivityCheckerAcceptsAnyReachableEndpoint(t *testing.T) {
	offline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer offline.Close()
	online := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer online.Close()

	checker := newConnectivityChecker([]string{offline.URL, online.URL})
	status := checker.Status(context.Background())
	if !status.Online || status.Status != connectivityOnline {
		t.Fatal("expected internet to be considered online when one endpoint is reachable")
	}
}

func TestConnectivityCheckerReportsUnknownBeforeOfflineIsConfirmed(t *testing.T) {
	offline := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer offline.Close()
	checker := newConnectivityChecker([]string{offline.URL})

	for failure := 1; failure <= 2; failure++ {
		checker.checkedAt = time.Time{}
		status := checker.Status(context.Background())
		if status.Online || status.Status != connectivityUnknown {
			t.Fatalf("status after %d failure(s) = %+v, want unknown", failure, status)
		}
	}
	checker.checkedAt = time.Time{}
	status := checker.Status(context.Background())
	if status.Online || status.Status != connectivityOffline {
		t.Fatalf("status after three failures = %+v, want offline", status)
	}
}

func TestConnectivityCheckerMarksTransientFailureAsUnknownAfterOnline(t *testing.T) {
	online := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if online {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	checker := newConnectivityChecker([]string{server.URL})
	if status := checker.Status(context.Background()); !status.Online || status.Status != connectivityOnline {
		t.Fatal("initial online check failed")
	}
	online = false
	for failure := 1; failure <= 2; failure++ {
		checker.checkedAt = time.Time{}
		status := checker.Status(context.Background())
		if !status.Online || status.Status != connectivityUnknown {
			t.Fatalf("status after %d failure(s) = %+v, want online compatibility with unknown status", failure, status)
		}
	}
	checker.checkedAt = time.Time{}
	status := checker.Status(context.Background())
	if status.Online || status.Status != connectivityOffline {
		t.Fatalf("status after three failures = %+v, want offline", status)
	}
}

func TestBuildNetworkStatusSeparatesLANAndWiFi(t *testing.T) {
	status := buildNetworkStatus([]networkInterfaceSnapshot{
		{Name: "eth0", Flags: net.FlagUp, Addresses: []net.Addr{testAddress("192.168.1.20/24"), testAddress("fe80::1/64")}},
		{Name: "wlan0", Flags: net.FlagUp, Addresses: []net.Addr{testAddress("192.168.1.21/24")}},
		{Name: "ztabc", Flags: net.FlagUp, Addresses: []net.Addr{testAddress("10.10.0.1/24")}},
		{Name: "lo", Flags: net.FlagUp | net.FlagLoopback, Addresses: []net.Addr{testAddress("127.0.0.1/8")}},
		{Name: "eth1", Addresses: []net.Addr{testAddress("192.168.2.20/24")}},
	}, "raspberry.local")

	if len(status.LAN) != 1 || status.LAN[0] != "192.168.1.20" {
		t.Fatalf("LAN = %#v", status.LAN)
	}
	if len(status.WiFi) != 1 || status.WiFi[0] != "192.168.1.21" {
		t.Fatalf("WiFi = %#v", status.WiFi)
	}
	if status.LocalName != "raspberry.local" {
		t.Fatalf("LocalName = %q", status.LocalName)
	}
}
