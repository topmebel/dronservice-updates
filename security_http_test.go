package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecureHTTPHandlerRequiresRemoteAuthentication(t *testing.T) {
	handler := secureHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), HTTPAccessConfig{Username: "operator", Password: "secret"})

	unauthorized := httptest.NewRequest(http.MethodGet, "http://dronservice.local/devices", nil)
	unauthorized.RemoteAddr = "192.0.2.10:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, unauthorized)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	authorized := httptest.NewRequest(http.MethodGet, "http://dronservice.local/devices", nil)
	authorized.RemoteAddr = "192.0.2.10:1234"
	authorized.SetBasicAuth("operator", "secret")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, authorized)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authenticated status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestSecureHTTPHandlerAllowsLoopbackHealthChecks(t *testing.T) {
	handler := secureHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), HTTPAccessConfig{})
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/update", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("loopback status = %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestSecureHTTPHandlerRejectsCrossOriginStateChange(t *testing.T) {
	handler := secureHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), HTTPAccessConfig{Username: "operator", Password: "secret"})
	request := httptest.NewRequest(http.MethodPost, "http://dronservice.local/api/update", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Host = "dronservice.local"
	request.Header.Set("Origin", "http://attacker.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	request.SetBasicAuth("operator", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestSecureHTTPHandlerAllowsSameOriginStateChange(t *testing.T) {
	handler := secureHTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), HTTPAccessConfig{Username: "operator", Password: "secret"})
	request := httptest.NewRequest(http.MethodPost, "http://dronservice.local/api/update", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Host = "dronservice.local"
	request.Header.Set("Origin", "http://dronservice.local")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	request.SetBasicAuth("operator", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("same-origin status = %d, want %d", response.Code, http.StatusNoContent)
	}
}
