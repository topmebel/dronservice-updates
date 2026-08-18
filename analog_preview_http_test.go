package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"DronService/internal/stream"
	"DronService/internal/streampreview"
)

type fakeAnalogPreviewSource struct {
	source      stream.Source
	err         error
	requestedID string
}

func (f *fakeAnalogPreviewSource) ResolveAnalogPreview(_ context.Context, deviceID string) (stream.Source, error) {
	f.requestedID = deviceID
	return f.source, f.err
}

type fakeAnalogPreviewSessions struct {
	session       streampreview.Session
	startErr      error
	stopErr       error
	startedOwner  string
	startedSource stream.Source
	stoppedOwner  string
	stoppedID     string
}

func (f *fakeAnalogPreviewSessions) StartSource(_ context.Context, ownerID string, source stream.Source) (streampreview.Session, error) {
	f.startedOwner = ownerID
	f.startedSource = source
	return f.session, f.startErr
}

func (f *fakeAnalogPreviewSessions) Stop(_ context.Context, ownerID, sessionID string) error {
	f.stoppedOwner = ownerID
	f.stoppedID = sessionID
	return f.stopErr
}

func TestAnalogPreviewStartReturnsCaptureMetadataWithoutInternalCommand(t *testing.T) {
	expiresAt := time.Date(2026, time.August, 15, 12, 0, 0, 0, time.UTC)
	source := stream.Source{
		ID: "analog:usb-xhci-hcd.1-1", Type: "analog", Name: "Передняя",
		DevicePath: "/dev/video2", PixelFormat: "MJPG", Resolution: "720x576", FPS: "10",
	}
	sources := &fakeAnalogPreviewSource{source: source}
	previews := &fakeAnalogPreviewSessions{session: streampreview.Session{
		ID: "session123", Path: "_dron_preview_deadbeef", CameraID: "analog:usb-xhci-hcd.1-1", ExpiresAt: expiresAt,
	}}
	handler := analogPreviewStartHandler(sources, previews, "http://192.168.1.147:8888")
	request := httptest.NewRequest(http.MethodPost, "/api/video-devices/usb-xhci-hcd.1-1/preview", nil)
	request.SetPathValue("deviceID", "usb-xhci-hcd.1-1")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if sources.requestedID != "usb-xhci-hcd.1-1" {
		t.Fatalf("resolved device = %q", sources.requestedID)
	}
	if previews.startedOwner != "analog:usb-xhci-hcd.1-1" || previews.startedSource != source {
		t.Fatalf("StartSource() owner=%q source=%+v", previews.startedOwner, previews.startedSource)
	}
	var result cameraPreviewResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	wantMetadata := previewStreamMetadata{Kind: "analog", PixelFormat: "MJPG", Resolution: "720x576", FPS: "10", BitrateMode: "CRF 23"}
	if result.SessionID != "session123" || result.URL != "http://192.168.1.147:8888/_dron_preview_deadbeef?autoplay=true&controls=true&muted=true&playsInline=true" || !result.ExpiresAt.Equal(expiresAt) || result.Stream != wantMetadata {
		t.Fatalf("response = %+v", result)
	}
	for _, internal := range []string{"/dev/video2", "/usr/bin/ffmpeg", "runOnDemand", "RTSP_PORT", "MTX_PATH"} {
		if strings.Contains(response.Body.String(), internal) {
			t.Errorf("response exposes %q: %s", internal, response.Body.String())
		}
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", response.Header().Get("Cache-Control"))
	}
}

func TestAnalogPreviewStartMapsResolverErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "FFmpeg missing", err: errAnalogPreviewUnavailable, want: http.StatusServiceUnavailable},
		{name: "device missing", err: errAnalogDeviceNotFound, want: http.StatusNotFound},
		{name: "configuration missing", err: errAnalogDeviceNotConfigured, want: http.StatusConflict},
		{name: "mode stale", err: errAnalogDeviceModeInvalid, want: http.StatusConflict},
		{name: "scan failed", err: errors.New("private ioctl detail"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previews := &fakeAnalogPreviewSessions{}
			handler := analogPreviewStartHandler(&fakeAnalogPreviewSource{err: tt.err}, previews, "http://192.168.1.147:8888")
			request := httptest.NewRequest(http.MethodPost, "/api/video-devices/camera/preview", nil)
			request.SetPathValue("deviceID", "camera")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d", response.Code, tt.want)
			}
			if previews.startedOwner != "" {
				t.Fatal("preview session was started")
			}
			if strings.Contains(response.Body.String(), "private ioctl detail") {
				t.Fatalf("response exposes internal error: %s", response.Body.String())
			}
		})
	}
}

func TestAnalogPreviewStartRejectsInvalidHLSBaseBeforeResolvingDevice(t *testing.T) {
	sources := &fakeAnalogPreviewSource{}
	previews := &fakeAnalogPreviewSessions{}
	handler := analogPreviewStartHandler(sources, previews, "http://user:secret@192.168.1.147:8888")
	request := httptest.NewRequest(http.MethodPost, "/api/video-devices/camera/preview", nil)
	request.SetPathValue("deviceID", "camera")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if sources.requestedID != "" || previews.startedOwner != "" {
		t.Fatal("invalid HLS base reached source or preview manager")
	}
}

func TestAnalogPreviewStartHidesMediaMTXFailure(t *testing.T) {
	sources := &fakeAnalogPreviewSource{source: stream.Source{Type: "analog", DevicePath: "/dev/video2", PixelFormat: "YUYV", Resolution: "720x576", FPS: "25"}}
	previews := &fakeAnalogPreviewSessions{startErr: errors.New("MediaMTX private response")}
	handler := analogPreviewStartHandler(sources, previews, "http://192.168.1.147:8888")
	request := httptest.NewRequest(http.MethodPost, "/api/video-devices/camera/preview", nil)
	request.SetPathValue("deviceID", "camera")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	if strings.Contains(response.Body.String(), "private response") {
		t.Fatalf("response exposes upstream error: %s", response.Body.String())
	}
}

func TestAnalogPreviewStopDeletesDeviceBoundSession(t *testing.T) {
	previews := &fakeAnalogPreviewSessions{}
	handler := analogPreviewStopHandler(previews)
	request := httptest.NewRequest(http.MethodDelete, "/api/video-devices/usb-camera/preview/session123", nil)
	request.SetPathValue("deviceID", "usb-camera")
	request.SetPathValue("sessionID", "session123")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if previews.stoppedOwner != "analog:usb-camera" || previews.stoppedID != "session123" {
		t.Fatalf("Stop() owner=%q session=%q", previews.stoppedOwner, previews.stoppedID)
	}
}

func TestAnalogPreviewStopMapsErrors(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want int
	}{
		{err: streampreview.ErrSessionNotFound, want: http.StatusNotFound},
		{err: errors.New("delete failed"), want: http.StatusBadGateway},
	} {
		handler := analogPreviewStopHandler(&fakeAnalogPreviewSessions{stopErr: tt.err})
		request := httptest.NewRequest(http.MethodDelete, "/api/video-devices/camera/preview/session123", nil)
		request.SetPathValue("deviceID", "camera")
		request.SetPathValue("sessionID", "session123")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tt.want {
			t.Fatalf("status = %d, want %d", response.Code, tt.want)
		}
	}
}
