package main

import (
	"encoding/json"
	stdhtml "html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"DronService/internal/ipcamera"
)

func TestIPCamerasPublicResponsesDoNotExposePersistedCredentials(t *testing.T) {
	service, err := ipcamera.NewService(t.TempDir(), ipcamera.DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const secret = "camera-secret-do-not-expose-73921"
	if err := service.Save(ipcamera.SaveRequest{
		ID:             "camera",
		Name:           "Front",
		Address:        "192.168.1.20",
		Manufacturer:   "Dahua",
		Username:       "admin",
		Password:       secret,
		MainStreamPath: "rtsp://admin:" + secret + "@192.168.1.20/main",
		SubStreamPath:  "rtsp://admin:" + secret + "@192.168.1.20/sub",
	}); err != nil {
		t.Fatal(err)
	}

	apiResponse := httptest.NewRecorder()
	ipCamerasHandler(service).ServeHTTP(apiResponse, httptest.NewRequest(http.MethodGet, "/api/ip-cameras", nil))
	if apiResponse.Code != http.StatusOK {
		t.Fatalf("GET /api/ip-cameras status = %d", apiResponse.Code)
	}
	if strings.Contains(apiResponse.Body.String(), secret) {
		t.Fatal("GET /api/ip-cameras exposes the saved password")
	}
	var payload struct {
		Cameras []map[string]any `json:"cameras"`
	}
	if err := json.Unmarshal(apiResponse.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode API response: %v", err)
	}
	if len(payload.Cameras) != 1 {
		t.Fatalf("cameras = %#v", payload.Cameras)
	}
	assertPublicCameraJSON(t, payload.Cameras[0])

	pageHandler, err := newIPCamerasPageHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	pageResponse := httptest.NewRecorder()
	pageHandler.ServeHTTP(pageResponse, httptest.NewRequest(http.MethodGet, "/ip-cameras", nil))
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("GET /ip-cameras status = %d", pageResponse.Code)
	}
	if strings.Contains(pageResponse.Body.String(), secret) {
		t.Fatal("GET /ip-cameras exposes the saved password in HTML")
	}
	match := regexp.MustCompile(`data-camera='([^']*)'`).FindStringSubmatch(pageResponse.Body.String())
	if len(match) != 2 {
		t.Fatal("rendered page does not contain data-camera")
	}
	var embedded map[string]any
	if err := json.Unmarshal([]byte(stdhtml.UnescapeString(match[1])), &embedded); err != nil {
		t.Fatalf("decode data-camera: %v", err)
	}
	assertPublicCameraJSON(t, embedded)
}

func assertPublicCameraJSON(t *testing.T, camera map[string]any) {
	t.Helper()
	if _, exists := camera["password"]; exists {
		t.Fatalf("public camera contains password: %#v", camera)
	}
	if camera["hasPassword"] != true {
		t.Fatalf("hasPassword = %#v, want true", camera["hasPassword"])
	}
	if camera["username"] != "admin" {
		t.Fatalf("username = %#v, want admin", camera["username"])
	}
	for _, field := range []string{"mainStreamPath", "subStreamPath"} {
		value, _ := camera[field].(string)
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "rtsp" && parsed.Scheme != "rtsps") || parsed.Host == "" || parsed.User != nil {
			t.Fatalf("%s exposes credentials: %q", field, value)
		}
	}
}
