package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"DronService/internal/ipcamera"
	"DronService/internal/streampreview"
)

type fakeCameraPreviewSource struct {
	source          ipcamera.StreamSource
	err             error
	requestedID     string
	requestedStream string
}

func (f *fakeCameraPreviewSource) PreviewStreamSource(cameraID, streamKind string) (ipcamera.StreamSource, error) {
	f.requestedID, f.requestedStream = cameraID, streamKind
	return f.source, f.err
}

type fakeCameraPreviewSessions struct {
	session       streampreview.Session
	startErr      error
	stopErr       error
	startedID     string
	startedURL    string
	stoppedID     string
	stopSessionID string
}

func (f *fakeCameraPreviewSessions) Start(_ context.Context, cameraID, sourceURL string) (streampreview.Session, error) {
	f.startedID, f.startedURL = cameraID, sourceURL
	return f.session, f.startErr
}

func (f *fakeCameraPreviewSessions) Stop(_ context.Context, cameraID, sessionID string) error {
	f.stoppedID, f.stopSessionID = cameraID, sessionID
	return f.stopErr
}

func TestCameraPreviewStartReturnsCredentialFreeHLSURL(t *testing.T) {
	expiresAt := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	previews := &fakeCameraPreviewSessions{session: streampreview.Session{
		ID: "session123", Path: "_dron_preview_deadbeef", CameraID: "camera", ExpiresAt: expiresAt,
	}}
	cameras := &fakeCameraPreviewSource{source: ipcamera.StreamSource{
		Kind: "sub", Metadata: ipcamera.VideoStream{Resolution: "704x576", FPS: "25", BitrateKbps: 512},
		URL: "rtsp://admin:secret@192.168.1.20/sub",
	}}
	handler := cameraPreviewStartHandler(cameras, previews, "http://192.168.1.147:8888")
	request := httptest.NewRequest(http.MethodPost, "/api/ip-cameras/camera/preview?stream=sub", nil)
	request.SetPathValue("cameraID", "camera")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if cameras.requestedID != "camera" || cameras.requestedStream != "sub" {
		t.Fatalf("PreviewStreamSource() camera=%q stream=%q", cameras.requestedID, cameras.requestedStream)
	}
	if previews.startedID != "camera" || previews.startedURL != "rtsp://admin:secret@192.168.1.20/sub" {
		t.Fatalf("Start() camera=%q source=%q", previews.startedID, previews.startedURL)
	}
	var result cameraPreviewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SessionID != "session123" || !result.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("response = %+v", result)
	}
	if result.Stream != (previewStreamMetadata{Kind: "sub", Resolution: "704x576", FPS: "25", BitrateKbps: 512}) {
		t.Fatalf("stream metadata = %+v", result.Stream)
	}
	parsed, err := url.Parse(result.URL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "http" || parsed.Host != "192.168.1.147:8888" || parsed.Path != "/_dron_preview_deadbeef" {
		t.Fatalf("preview URL = %q", result.URL)
	}
	for _, key := range []string{"autoplay", "controls", "muted", "playsInline"} {
		if parsed.Query().Get(key) != "true" {
			t.Errorf("preview URL query %s = %q", key, parsed.Query().Get(key))
		}
	}
	for _, secret := range []string{"rtsp://", "admin", "secret", "192.168.1.20"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Errorf("response exposes %q: %s", secret, response.Body.String())
		}
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
}

func TestCameraPreviewStartDefaultsToMainStream(t *testing.T) {
	cameras := &fakeCameraPreviewSource{source: ipcamera.StreamSource{URL: "rtsp://camera/main"}}
	previews := &fakeCameraPreviewSessions{session: streampreview.Session{ID: "session", Path: "_dron_preview_token"}}
	handler := cameraPreviewStartHandler(cameras, previews, "http://192.168.1.147:8888")
	request := httptest.NewRequest(http.MethodPost, "/api/ip-cameras/camera/preview", nil)
	request.SetPathValue("cameraID", "camera")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || cameras.requestedStream != "main" {
		t.Fatalf("status=%d stream=%q", response.Code, cameras.requestedStream)
	}
}

func TestCameraPreviewStartRejectsUnknownStream(t *testing.T) {
	cameras := &fakeCameraPreviewSource{source: ipcamera.StreamSource{URL: "rtsp://camera/main"}}
	previews := &fakeCameraPreviewSessions{}
	handler := cameraPreviewStartHandler(cameras, previews, "http://192.168.1.147:8888")
	request := httptest.NewRequest(http.MethodPost, "/api/ip-cameras/camera/preview?stream=third", nil)
	request.SetPathValue("cameraID", "camera")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if cameras.requestedID != "" || previews.startedID != "" {
		t.Fatal("invalid stream reached camera source or preview manager")
	}
}

func TestCameraPreviewStartMapsSourceErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: ipcamera.ErrCameraNotFound, want: http.StatusNotFound},
		{name: "initialization required", err: ipcamera.ErrDahuaInitializationRequired, want: http.StatusConflict},
		{name: "stream missing", err: errors.New("missing stream"), want: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previews := &fakeCameraPreviewSessions{}
			handler := cameraPreviewStartHandler(&fakeCameraPreviewSource{err: tt.err}, previews, "http://192.168.1.147:8888")
			request := httptest.NewRequest(http.MethodPost, "/api/ip-cameras/camera/preview", nil)
			request.SetPathValue("cameraID", "camera")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d", response.Code, tt.want)
			}
			if previews.startedID != "" {
				t.Fatal("preview session was started")
			}
		})
	}
}

func TestCameraPreviewStartRejectsInvalidHLSBaseBeforeStartingSession(t *testing.T) {
	previews := &fakeCameraPreviewSessions{}
	handler := cameraPreviewStartHandler(&fakeCameraPreviewSource{source: ipcamera.StreamSource{URL: "rtsp://camera/main"}}, previews, "http://user:pass@192.168.1.147:8888")
	request := httptest.NewRequest(http.MethodPost, "/api/ip-cameras/camera/preview", nil)
	request.SetPathValue("cameraID", "camera")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if previews.startedID != "" {
		t.Fatal("preview session was started")
	}
}

func TestCameraPreviewStartMapsMediaMTXFailure(t *testing.T) {
	previews := &fakeCameraPreviewSessions{startErr: errors.New("MediaMTX unavailable")}
	handler := cameraPreviewStartHandler(&fakeCameraPreviewSource{source: ipcamera.StreamSource{URL: "rtsp://camera/main"}}, previews, "http://192.168.1.147:8888")
	request := httptest.NewRequest(http.MethodPost, "/api/ip-cameras/camera/preview", nil)
	request.SetPathValue("cameraID", "camera")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	if strings.Contains(response.Body.String(), "MediaMTX unavailable") {
		t.Fatalf("response exposes upstream error: %s", response.Body.String())
	}
}

func TestCameraPreviewStopDeletesCameraBoundSession(t *testing.T) {
	previews := &fakeCameraPreviewSessions{}
	handler := cameraPreviewStopHandler(previews)
	request := httptest.NewRequest(http.MethodDelete, "/api/ip-cameras/camera/preview/session123", nil)
	request.SetPathValue("cameraID", "camera")
	request.SetPathValue("sessionID", "session123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if previews.stoppedID != "camera" || previews.stopSessionID != "session123" {
		t.Fatalf("Stop() camera=%q session=%q", previews.stoppedID, previews.stopSessionID)
	}
}

func TestCameraPreviewStopMapsErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: streampreview.ErrSessionNotFound, want: http.StatusNotFound},
		{name: "MediaMTX unavailable", err: errors.New("delete failed"), want: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := cameraPreviewStopHandler(&fakeCameraPreviewSessions{stopErr: tt.err})
			request := httptest.NewRequest(http.MethodDelete, "/api/ip-cameras/camera/preview/session123", nil)
			request.SetPathValue("cameraID", "camera")
			request.SetPathValue("sessionID", "session123")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d", response.Code, tt.want)
			}
		})
	}
}

func TestCameraPreviewHLSURLSupportsBasePath(t *testing.T) {
	got, err := cameraPreviewHLSURL("https://video.example.test/hls/", "camera_1")
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(got)
	if parsed.Path != "/hls/camera_1" || parsed.Query().Get("playsInline") != "true" {
		t.Fatalf("URL = %q", got)
	}
}
