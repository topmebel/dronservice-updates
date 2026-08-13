package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"DronService/internal/mediamtx"
)

func TestApplySourceRenamesMediaMTXPath(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	service := NewService(mediamtx.NewClient(server.URL, "", ""))
	err := service.ApplySource(context.Background(), Config{Name: "camera_new"}, Source{Type: "ip", Input: "rtsp://192.168.1.20/main"}, "camera_old")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"POST /v3/config/paths/add/camera_new",
		"DELETE /v3/config/paths/delete/camera_old",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestFromMediaMTXPathState(t *testing.T) {
	tests := []struct {
		name string
		path mediamtx.Path
		want State
	}{
		{name: "on-demand path is idle", path: mediamtx.Path{Name: "camera1"}, want: StateIdle},
		{name: "online path", path: mediamtx.Path{Online: true}, want: StateOnline},
		{name: "ready path", path: mediamtx.Path{Ready: true}, want: StateOnline},
		{name: "available path", path: mediamtx.Path{Available: true}, want: StateOnline},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromMediaMTXPath(tt.path).State; got != tt.want {
				t.Fatalf("state = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMediaMTXUpdateProxiesIPCameraWithoutTranscoding(t *testing.T) {
	update, err := mediaMTXUpdate("front_camera", Source{Type: "ip", Input: "rtsp://admin:secret@192.168.1.20/main"})
	if err != nil {
		t.Fatal(err)
	}
	if update.Source != "rtsp://admin:secret@192.168.1.20/main" || !update.SourceOnDemand || update.RunOnDemand != "" {
		t.Fatalf("update = %+v", update)
	}
}

func TestMediaMTXUpdateTranscodesAnalogCameraToH264(t *testing.T) {
	update, err := mediaMTXUpdate("analog_1", Source{Type: "analog", DevicePath: "/dev/video2", PixelFormat: "MJPG", Resolution: "720x576", FPS: "25"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"-f v4l2", "-input_format mjpeg", "-c:v libx264", "rtsp://127.0.0.1:554/analog_1"} {
		if !strings.Contains(update.RunOnDemand, expected) {
			t.Fatalf("runOnDemand does not contain %q: %s", expected, update.RunOnDemand)
		}
	}
	if update.Source != "publisher" || !update.RunOnDemandRestart || update.SourceOnDemand {
		t.Fatalf("update = %+v", update)
	}
}

func TestMediaMTXUpdateRejectsUnsafeAnalogValues(t *testing.T) {
	_, err := mediaMTXUpdate("camera;bad", Source{Type: "analog", DevicePath: "/dev/video0;reboot", PixelFormat: "MJPG", Resolution: "720x576", FPS: "25"})
	if err == nil {
		t.Fatal("unsafe stream configuration was accepted")
	}
}

func TestFromMediaMTXPathMapsPublicFields(t *testing.T) {
	path := mediamtx.Path{
		Name:          "camera1",
		Readers:       []mediamtx.Reader{{}, {}},
		InboundBytes:  12,
		OutboundBytes: 34,
	}

	got := fromMediaMTXPath(path)
	if got.Name != "camera1" || got.Readers != 2 || got.InboundBytes != 12 || got.OutboundBytes != 34 {
		t.Fatalf("stream = %#v", got)
	}
}

func TestSourceWithoutCredentials(t *testing.T) {
	const source = "rtsp://admin:secret@192.168.1.20:554/stream1"
	if got := sourceWithoutCredentials(source); got != "rtsp://192.168.1.20:554/stream1" {
		t.Fatalf("sourceWithoutCredentials() = %q", got)
	}
	if !hasURLCredentials(source) {
		t.Fatal("hasURLCredentials() = false")
	}
}
