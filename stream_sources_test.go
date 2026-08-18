package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"DronService/internal/deviceconfig"
	"DronService/internal/ipcamera"
	"DronService/internal/stream"
	"DronService/internal/v4l2"
)

type fakeVideoDeviceScanner struct {
	devices []v4l2.Device
	err     error
}

func (f fakeVideoDeviceScanner) Scan(context.Context) ([]v4l2.Device, error) {
	return f.devices, f.err
}

func executableForTest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func configuredAnalogDevice() v4l2.Device {
	return v4l2.Device{
		ID:   "usb-xhci-hcd.1-1",
		Path: "/dev/video2",
		Formats: []v4l2.Format{{
			PixelFormat: "MJPG",
			Modes:       []v4l2.Mode{{Resolution: "720x576", FPS: "10"}},
		}},
	}
}

func TestResolveAnalogPreviewUsesCurrentDevicePathAndIgnoresUse(t *testing.T) {
	store, err := deviceconfig.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config := deviceconfig.Config{
		DeviceID:    "usb-xhci-hcd.1-1",
		DevicePath:  "/dev/video9",
		Name:        "Передняя",
		PixelFormat: "MJPG",
		Resolution:  "720x576",
		FPS:         "10",
		Use:         false,
	}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	catalog := streamSourceCatalog{
		scanner:    fakeVideoDeviceScanner{devices: []v4l2.Device{configuredAnalogDevice()}},
		devices:    store,
		ffmpegPath: executableForTest(t),
	}

	source, err := catalog.ResolveAnalogPreview(context.Background(), config.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if source.Type != "analog" || source.ID != "analog:"+config.DeviceID || source.Name != config.Name {
		t.Fatalf("source identity = %+v", source)
	}
	if source.DevicePath != "/dev/video2" || source.PixelFormat != "MJPG" || source.Resolution != "720x576" || source.FPS != "10" {
		t.Fatalf("source capture settings = %+v", source)
	}
}

func TestResolveAnalogPreviewRequiresConnectedConfiguredCurrentMode(t *testing.T) {
	device := configuredAnalogDevice()
	tests := []struct {
		name    string
		devices []v4l2.Device
		save    *deviceconfig.Config
		want    error
	}{
		{name: "device missing", devices: nil, save: &deviceconfig.Config{DeviceID: device.ID, Name: "Camera", PixelFormat: "MJPG", Resolution: "720x576", FPS: "10"}, want: errAnalogDeviceNotFound},
		{name: "configuration missing", devices: []v4l2.Device{device}, want: errAnalogDeviceNotConfigured},
		{name: "mode no longer available", devices: []v4l2.Device{device}, save: &deviceconfig.Config{DeviceID: device.ID, Name: "Camera", PixelFormat: "MJPG", Resolution: "1920x1080", FPS: "30"}, want: errAnalogDeviceModeInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := deviceconfig.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if tt.save != nil {
				if err := store.Save(*tt.save); err != nil {
					t.Fatal(err)
				}
			}
			catalog := streamSourceCatalog{scanner: fakeVideoDeviceScanner{devices: tt.devices}, devices: store, ffmpegPath: executableForTest(t)}
			if _, err := catalog.ResolveAnalogPreview(context.Background(), device.ID); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestResolveAnalogPreviewRejectsUnsupportedCaptureFormat(t *testing.T) {
	device := configuredAnalogDevice()
	device.Formats = []v4l2.Format{{PixelFormat: "NV12", Modes: []v4l2.Mode{{Resolution: "720x576", FPS: "25"}}}}
	store, err := deviceconfig.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(deviceconfig.Config{DeviceID: device.ID, Name: "Camera", PixelFormat: "NV12", Resolution: "720x576", FPS: "25"}); err != nil {
		t.Fatal(err)
	}
	catalog := streamSourceCatalog{scanner: fakeVideoDeviceScanner{devices: []v4l2.Device{device}}, devices: store, ffmpegPath: executableForTest(t)}
	if _, err := catalog.ResolveAnalogPreview(context.Background(), device.ID); !errors.Is(err, errAnalogDeviceModeInvalid) {
		t.Fatalf("error = %v, want %v", err, errAnalogDeviceModeInvalid)
	}
}

func TestResolveAnalogPreviewChecksFFmpegBeforeCreatingSource(t *testing.T) {
	catalog := streamSourceCatalog{
		scanner:    fakeVideoDeviceScanner{err: errors.New("must not scan")},
		devices:    nil,
		ffmpegPath: filepath.Join(t.TempDir(), "missing-ffmpeg"),
	}
	if _, err := catalog.ResolveAnalogPreview(context.Background(), "camera"); !errors.Is(err, errAnalogPreviewUnavailable) {
		t.Fatalf("error = %v, want %v", err, errAnalogPreviewUnavailable)
	}
}

func TestResolveAnalogPreviewWrapsScanFailure(t *testing.T) {
	store, err := deviceconfig.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	catalog := streamSourceCatalog{scanner: fakeVideoDeviceScanner{err: errors.New("ioctl failed")}, devices: store, ffmpegPath: executableForTest(t)}
	_, err = catalog.ResolveAnalogPreview(context.Background(), "camera")
	if err == nil || errors.Is(err, errAnalogDeviceNotFound) || err.Error() != "scan analog camera for preview: ioctl failed" {
		t.Fatalf("error = %v", err)
	}
}

func TestDecorateStreamConfigsCopiesDronServiceSourceSettings(t *testing.T) {
	configs := []stream.Config{
		{Name: "front_main", Source: "rtsp://192.168.1.20/main"},
		{Name: "capture", Source: "publisher", RunOnDemand: "/usr/bin/ffmpeg -f v4l2 -input_format mjpeg -video_size 720x576 -framerate 10 -i /dev/video2 -f rtsp output"},
	}
	sources := []stream.Source{
		{
			ID: "ip:front:main", Type: "ip", Name: "Front", Detail: "Main stream",
			Input: "rtsp://admin:secret@192.168.1.20/main", Resolution: "1920x1080", FPS: "25", BitrateKbps: 4096,
		},
		{
			ID: "analog:usb-capture", Type: "analog", Name: "Capture", DevicePath: "/dev/video2",
			Resolution: "720x576", FPS: "10",
		},
	}

	got := decorateStreamConfigs(configs, sources, "rtsp://192.168.1.147:554")
	if got[0].SourceID != "ip:front:main" || got[0].Resolution != "1920x1080" || got[0].FPS != "25" || got[0].BitrateKbps != 4096 {
		t.Fatalf("IP stream config = %+v", got[0])
	}
	if got[1].SourceID != "analog:usb-capture" || got[1].Resolution != "720x576" || got[1].FPS != "10" || got[1].BitrateKbps != 0 {
		t.Fatalf("analog stream config = %+v", got[1])
	}

	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "admin") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("decorated public config exposes RTSP credentials: %s", encoded)
	}
	for _, field := range []string{`"resolution":"1920x1080"`, `"fps":"25"`, `"bitrateKbps":4096`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("decorated public config is missing %s: %s", field, encoded)
		}
	}
}

func TestDecorateAnalogStreamUsesModeEncodedInExistingMediaMTXPath(t *testing.T) {
	configs := []stream.Config{{
		Name: "capture", Source: "publisher",
		RunOnDemand: "/usr/bin/ffmpeg -nostdin -f v4l2 -input_format yuyv422 -video_size 720x576 -framerate 10 -i /dev/video2 -c:v libx264 -f rtsp output",
	}}
	sources := []stream.Source{{
		ID: "analog:usb-capture", Type: "analog", Name: "Capture", DevicePath: "/dev/video2",
		PixelFormat: "MJPG", Resolution: "1920x1080", FPS: "30", BitrateKbps: 999,
	}}

	got := decorateStreamConfigs(configs, sources, "rtsp://192.168.1.147:554")
	if got[0].SourceID != "analog:usb-capture" || got[0].SourceName != "Capture" {
		t.Fatalf("analog source identity = %+v", got[0])
	}
	if got[0].Resolution != "720x576" || got[0].FPS != "10" {
		t.Fatalf("analog path settings = %+v, want existing 720x576 at 10 FPS", got[0])
	}
	if got[0].BitrateKbps != 0 {
		t.Fatalf("analog path bitrate = %d, want unknown", got[0].BitrateKbps)
	}
}

func TestDecorateAnalogStreamFallsBackForUnavailableCommandSettings(t *testing.T) {
	configs := []stream.Config{{
		Name: "capture", Source: "publisher",
		RunOnDemand: "/usr/bin/ffmpeg -f v4l2 -input_format mjpeg -video_size invalid -framerate invalid -i /dev/video2 -f rtsp output",
	}}
	sources := []stream.Source{{
		ID: "analog:usb-capture", Type: "analog", Name: "Capture", DevicePath: "/dev/video2",
		Resolution: "1920x1080", FPS: "30",
	}}

	got := decorateStreamConfigs(configs, sources, "rtsp://192.168.1.147:554")
	if got[0].SourceID != "analog:usb-capture" || got[0].Resolution != "1920x1080" || got[0].FPS != "30" {
		t.Fatalf("analog fallback settings = %+v", got[0])
	}
}

func TestParseAnalogRunOnDemandRejectsAmbiguousOrUnsafeIdentity(t *testing.T) {
	tests := []string{
		"/bin/sh -f v4l2 -input_format mjpeg -i /dev/video2",
		"/usr/bin/ffmpeg -f v4l2 -input_format h264 -i /dev/video2",
		"/usr/bin/ffmpeg -f v4l2 -input_format mjpeg -i /dev/video2;reboot",
		"/usr/bin/ffmpeg -f v4l2 -input_format mjpeg -i /dev/video2 -i /dev/video4",
	}
	for _, command := range tests {
		if settings, ok := parseAnalogRunOnDemand(command); ok {
			t.Errorf("parseAnalogRunOnDemand(%q) = %+v, true", command, settings)
		}
	}
}

func TestStreamSourceJSONExposesSettingsButNotInternalInput(t *testing.T) {
	source := stream.Source{
		ID: "ip:front:sub", Type: "ip", Name: "Front", Detail: "Sub stream",
		Input: "rtsp://admin:secret@192.168.1.20/sub", Resolution: "704x576", FPS: "15", BitrateKbps: 512,
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	value := string(encoded)
	for _, field := range []string{`"resolution":"704x576"`, `"fps":"15"`, `"bitrateKbps":512`} {
		if !strings.Contains(value, field) {
			t.Fatalf("public source is missing %s: %s", field, value)
		}
	}
	if strings.Contains(value, "admin") || strings.Contains(value, "secret") || strings.Contains(value, "192.168.1.20") {
		t.Fatalf("public source exposes internal RTSP input: %s", value)
	}
}

func TestStreamSourceCatalogPreservesIPMainAndSubSettings(t *testing.T) {
	dataDir := t.TempDir()
	camerasJSON := `{"front":{"id":"front","name":"Front","address":"192.168.1.20","username":"admin","password":"secret","mainStreamPath":"rtsp://192.168.1.20/main","subStreamPath":"rtsp://192.168.1.20/sub","mainStream":{"resolution":"1920x1080","fps":"25","bitrateKbps":4096},"subStream":{"resolution":"704x576","fps":"15","bitrateKbps":512},"use":true}}`
	if err := os.WriteFile(filepath.Join(dataDir, "ip-cameras.json"), []byte(camerasJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	ipCameras, err := ipcamera.NewService(dataDir, ipcamera.DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	devices, err := deviceconfig.NewStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	catalog := streamSourceCatalog{scanner: fakeVideoDeviceScanner{}, devices: devices, ipCameras: ipCameras}

	sources, err := catalog.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]stream.Source, len(sources))
	for _, source := range sources {
		byID[source.ID] = source
	}
	main := byID["ip:front:main"]
	if main.Resolution != "1920x1080" || main.FPS != "25" || main.BitrateKbps != 4096 {
		t.Fatalf("main source settings = %+v", main)
	}
	sub := byID["ip:front:sub"]
	if sub.Resolution != "704x576" || sub.FPS != "15" || sub.BitrateKbps != 512 {
		t.Fatalf("sub source settings = %+v", sub)
	}
}
