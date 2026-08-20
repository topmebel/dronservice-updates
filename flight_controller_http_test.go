package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"DronService/internal/mavlink"
)

func TestFlightControllerPageShowsTelemetryAndConfig(t *testing.T) {
	for _, fragment := range []string{
		`href="/flight-controller">Автопилот`,
		`fetch('/api/flight-controller/status'`,
		`fetch('/api/flight-controller/config'`,
		`id="fc-config-form"`,
		`mavlink.json`,
		`app-shell`,
		`src="/assets/application-status.js" defer`,
	} {
		if !strings.Contains(flightControllerPageHTML, fragment) {
			t.Errorf("flight controller page does not contain %q", fragment)
		}
	}
}

func TestFlightControllerHTTPHandlers(t *testing.T) {
	store, err := mavlink.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := mavlink.NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	configResponse := httptest.NewRecorder()
	flightControllerConfigHandler(service).ServeHTTP(configResponse, httptest.NewRequest(http.MethodGet, "/api/flight-controller/config", nil))
	if configResponse.Code != http.StatusOK {
		t.Fatalf("GET config status = %d, want 200", configResponse.Code)
	}

	statusResponse := httptest.NewRecorder()
	flightControllerStatusHandler(service).ServeHTTP(statusResponse, httptest.NewRequest(http.MethodGet, "/api/flight-controller/status", nil))
	if statusResponse.Code != http.StatusOK {
		t.Fatalf("GET status status = %d, want 200", statusResponse.Code)
	}

	body, err := json.Marshal(map[string]any{
		"enabled":         true,
		"transport":       "udp",
		"udpAddr":         "0.0.0.0:14550",
		"outSystemId":     255,
		"targetSystemId":  0,
		"linkTimeout":     "5s",
		"messageInterval": "500ms",
	})
	if err != nil {
		t.Fatal(err)
	}
	updateRequest := httptest.NewRequest(http.MethodPut, "/api/flight-controller/config", bytes.NewReader(body))
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	flightControllerConfigHandler(service).ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("PUT config status = %d, want 200; body=%s", updateResponse.Code, updateResponse.Body.String())
	}

	updated := service.Config()
	if !updated.Enabled || updated.UDPAddr != "0.0.0.0:14550" {
		t.Fatalf("updated config = %#v", updated)
	}
	if updated.LinkTimeout != 5*time.Second {
		t.Fatalf("LinkTimeout = %s, want 5s", updated.LinkTimeout)
	}
}

func TestFlightControllerConfigHandlerRejectsInvalidTransport(t *testing.T) {
	store, err := mavlink.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := mavlink.NewService(store)
	if err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"enabled":true,"transport":"serial","udpAddr":"0.0.0.0:14550","outSystemId":255,"linkTimeout":"5s","messageInterval":"500ms"}`)
	request := httptest.NewRequest(http.MethodPut, "/api/flight-controller/config", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	flightControllerConfigHandler(service).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
}
