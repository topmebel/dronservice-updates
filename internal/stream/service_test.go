package stream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"DronService/internal/mediamtx"
)

const testInternalPreviewPath = InternalPreviewPathPrefix + "0123456789abcdef01234567"

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
	for _, expected := range []string{"-nostdin", "-f v4l2", "-input_format mjpeg", "-video_size 720x576", "-framerate 25", "-map 0:v:0", "-c:v libx264", "-crf 23", "-threads 2", `rtsp://127.0.0.1:$RTSP_PORT/$MTX_PATH`} {
		if !strings.Contains(update.RunOnDemand, expected) {
			t.Fatalf("runOnDemand does not contain %q: %s", expected, update.RunOnDemand)
		}
	}
	if update.Source != "publisher" || !update.RunOnDemandRestart || update.SourceOnDemand {
		t.Fatalf("update = %+v", update)
	}
}

func TestMediaMTXUpdateUsesYUYVInputWithoutRestartingTemporaryPreview(t *testing.T) {
	update, err := mediaMTXUpdate(testInternalPreviewPath, Source{Type: "analog", DevicePath: "/dev/video0", PixelFormat: "YUYV", Resolution: "720x576", FPS: "25"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"-input_format yuyv422", "-g 25", "-keyint_min 25"} {
		if !strings.Contains(update.RunOnDemand, expected) {
			t.Fatalf("runOnDemand does not contain %q: %s", expected, update.RunOnDemand)
		}
	}
	if update.RunOnDemandRestart {
		t.Fatal("temporary analog preview must not restart a failed FFmpeg command")
	}
	if update.RunOnDemandStartTimeout != "15s" || update.RunOnDemandCloseAfter != "2s" {
		t.Fatalf("temporary preview timeouts = start %q close %q", update.RunOnDemandStartTimeout, update.RunOnDemandCloseAfter)
	}
}

func TestMediaMTXUpdateRestartsPrefixLookalikePath(t *testing.T) {
	update, err := mediaMTXUpdate(InternalPreviewPathPrefix+"operator", Source{Type: "analog", DevicePath: "/dev/video0", PixelFormat: "YUYV", Resolution: "720x576", FPS: "25"})
	if err != nil {
		t.Fatal(err)
	}
	if !update.RunOnDemandRestart {
		t.Fatal("ordinary path sharing the preview prefix must retain restart behavior")
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

func TestListInternalPreviewPathsReturnsOnlyReservedNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v3/config/paths/list" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"name":"camera1","source":"rtsp://camera/main"},{"name":"_dron_preview_operator","source":"rtsp://camera/operator"},{"name":"_dron_preview_0123456789abcdef01234567","source":"rtsp://admin:secret@camera/main"},{"name":"_dron_preview_0123456789ABCDEF01234567","source":"rtsp://camera/uppercase"}]}`))
	}))
	defer server.Close()

	service := NewService(mediamtx.NewClient(server.URL, "", ""))
	paths, err := service.ListInternalPreviewPaths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 1 || paths[0] != testInternalPreviewPath {
		t.Fatalf("paths = %v", paths)
	}
}

func TestPublicListsHideInternalPreviewPaths(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/config/paths/list":
			_, _ = w.Write([]byte(`{"items":[{"name":"camera1","source":"rtsp://camera/main"},{"name":"_dron_preview_operator","source":"rtsp://camera/operator"},{"name":"_dron_preview_0123456789abcdef01234567","source":"rtsp://admin:secret@camera/main"}]}`))
		case "/v3/paths/list":
			_, _ = w.Write([]byte(`{"items":[{"name":"camera1","readers":[]},{"name":"_dron_preview_operator","readers":[]},{"name":"_dron_preview_0123456789abcdef01234567","online":true,"readers":[]}]}`))
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	service := NewService(mediamtx.NewClient(server.URL, "", ""))
	configs, err := service.ListConfigs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 2 || configs[0].Name != "camera1" || configs[1].Name != InternalPreviewPathPrefix+"operator" {
		t.Fatalf("configs = %+v", configs)
	}
	streams, err := service.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 2 || streams[0].Name != "camera1" || streams[1].Name != InternalPreviewPathPrefix+"operator" {
		t.Fatalf("streams = %+v", streams)
	}
}

func TestDeleteConfigTreatsMissingInternalPreviewAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	service := NewService(mediamtx.NewClient(server.URL, "", ""))
	if err := service.DeleteConfig(context.Background(), testInternalPreviewPath); err != nil {
		t.Fatalf("DeleteConfig(internal missing) error = %v", err)
	}
	if err := service.DeleteConfig(context.Background(), InternalPreviewPathPrefix+"operator"); err == nil {
		t.Fatal("DeleteConfig(prefix lookalike missing) error = nil")
	}
	if err := service.DeleteConfig(context.Background(), "camera1"); err == nil {
		t.Fatal("DeleteConfig(public missing) error = nil")
	}
}

func TestIsInternalPreviewPathMatchesGeneratedShapeOnly(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: testInternalPreviewPath, want: true},
		{name: InternalPreviewPathPrefix + "operator"},
		{name: InternalPreviewPathPrefix + "0123456789ABCDEF01234567"},
		{name: InternalPreviewPathPrefix + "0123456789abcdef0123456"},
		{name: InternalPreviewPathPrefix + "0123456789abcdef012345678"},
		{name: "camera1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsInternalPreviewPath(tt.name); got != tt.want {
				t.Fatalf("IsInternalPreviewPath(%q) = %t, want %t", tt.name, got, tt.want)
			}
		})
	}
}
