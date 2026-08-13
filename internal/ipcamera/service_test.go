package ipcamera

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServiceSavePreservesPassword(t *testing.T) {
	service, err := NewService(t.TempDir(), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request := SaveRequest{ID: "uuid:camera", Name: "Камера", Address: "192.168.1.20", Manufacturer: "Dahua", Username: "admin", Password: "secret", MainStreamPath: "rtsp://192.168.1.20/main", SubStreamPath: "rtsp://192.168.1.20/sub"}
	if err := service.Save(request); err != nil {
		t.Fatal(err)
	}
	request.Name = "Камера новая"
	request.Password = ""
	if err := service.Save(request); err != nil {
		t.Fatal(err)
	}
	if got := service.cameras[request.ID]; got.Password != "secret" || got.Name != request.Name {
		t.Fatalf("saved camera = %#v", got)
	}

	info, err := os.Stat(filepath.Join(filepath.Dir(service.filePath), "ip-cameras.json"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("ip-cameras.json permissions = %o, want 600", permissions)
	}
}

func TestCameraViewExposesPasswordInRTSPPaths(t *testing.T) {
	saved := persistedCamera{
		ID:             "camera",
		Address:        "192.168.1.20",
		Username:       "admin",
		Password:       "p@ss word",
		MainStreamPath: "rtsp://192.168.1.20/main",
		SubStreamPath:  "rtsp://192.168.1.20/sub",
	}

	camera := cameraFromPersisted(saved, true)
	if camera.Password != saved.Password {
		t.Fatalf("Password = %q, want %q", camera.Password, saved.Password)
	}
	if camera.MainStreamPath != "rtsp://admin:p%40ss%20word@192.168.1.20/main" {
		t.Fatalf("MainStreamPath = %q", camera.MainStreamPath)
	}
	if camera.SubStreamPath != "rtsp://admin:p%40ss%20word@192.168.1.20/sub" {
		t.Fatalf("SubStreamPath = %q", camera.SubStreamPath)
	}
	data, err := json.Marshal(camera)
	if err != nil {
		t.Fatalf("marshal camera: %v", err)
	}
	var response map[string]any
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatalf("decode camera response: %v", err)
	}
	if response["password"] != saved.Password || response["mainStreamPath"] != camera.MainStreamPath {
		t.Fatalf("camera response = %#v", response)
	}
	if _, exists := response["mainStreamUrl"]; exists {
		t.Fatalf("camera response contains redundant mainStreamUrl: %#v", response)
	}
}

func TestServiceExtractsCredentialsFromRTSPPaths(t *testing.T) {
	service, err := NewService(t.TempDir(), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request := SaveRequest{
		ID:             "camera",
		Name:           "Camera",
		Address:        "192.168.1.20",
		Manufacturer:   "Dahua",
		MainStreamPath: "rtsp://admin:p%40ss@192.168.1.20/main",
		SubStreamPath:  "rtsp://admin:p%40ss@192.168.1.20/sub",
	}
	if err := service.Save(request); err != nil {
		t.Fatal(err)
	}

	saved := service.cameras[request.ID]
	if saved.Username != "admin" || saved.Password != "p@ss" {
		t.Fatalf("saved credentials = %q/%q", saved.Username, saved.Password)
	}
	if saved.MainStreamPath != "rtsp://192.168.1.20/main" || saved.SubStreamPath != "rtsp://192.168.1.20/sub" {
		t.Fatalf("saved RTSP paths = %q, %q", saved.MainStreamPath, saved.SubStreamPath)
	}
}

func TestServiceRejectsInvalidRTSPPath(t *testing.T) {
	service, err := NewService(t.TempDir(), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = service.Save(SaveRequest{ID: "camera", Name: "Camera", Address: "192.168.1.20", Manufacturer: "Dahua", MainStreamPath: "http://example.com", SubStreamPath: "rtsp://192.168.1.20/sub"})
	if err == nil {
		t.Fatal("Save() error = nil")
	}
}

func TestVendorRTSPPaths(t *testing.T) {
	tests := []struct{ manufacturer, mainPath, subPath string }{
		{"Hikvision", "/Streaming/Channels/101", "/Streaming/Channels/102"},
		{"UNV", "/unicast/c1/s0/live", "/unicast/c1/s1/live"},
	}
	for _, tt := range tests {
		mainStream, subStream := defaultRTSPPaths(tt.manufacturer, net.ParseIP("192.168.1.20"))
		if mainStream != "rtsp://192.168.1.20:554"+tt.mainPath || subStream != "rtsp://192.168.1.20:554"+tt.subPath {
			t.Fatalf("%s paths = %q, %q", tt.manufacturer, mainStream, subStream)
		}
	}
}

func TestDahuaManufacturerAndDefaultRTSPPaths(t *testing.T) {
	device := DahuaDevice{Model: "DH-IPC-HFW1230S1", IP: net.ParseIP("192.168.106.108")}
	manufacturer := detectManufacturer(device)
	if manufacturer != "Dahua" {
		t.Fatalf("manufacturer = %q, want Dahua", manufacturer)
	}
	mainStream, subStream := defaultRTSPPaths(manufacturer, device.IP)
	if mainStream != "rtsp://192.168.106.108:554/cam/realmonitor?channel=1&subtype=0" {
		t.Fatalf("main stream = %q", mainStream)
	}
	if subStream != "rtsp://192.168.106.108:554/cam/realmonitor?channel=1&subtype=1" {
		t.Fatalf("sub stream = %q", subStream)
	}
}

func TestUNVXMLUpdatesExistingARPRecordWithoutChangingUserName(t *testing.T) {
	const id = "mac:88:26:3f:7e:7d:da"
	saved := updateGenericDiscoveredCamera(persistedCamera{}, id, DiscoveredDevice{
		Vendor:    "UNV",
		Protocols: []string{"UNV-ARP"},
		MAC:       "88263f7e7dda",
		IP:        net.ParseIP("192.168.4.107"),
	}, time.Now())
	saved = updateGenericDiscoveredCamera(saved, id, DiscoveredDevice{
		Vendor:     "UNV",
		Protocols:  []string{"UNV-UDP-3702"},
		MAC:        "88-26-3F-7E-7D-DA",
		IP:         net.ParseIP("192.168.4.107"),
		Model:      "IPC2124LB-ADF28KM-H",
		DeviceName: "IPC2124LB-ADF28KM-H",
	}, time.Now())

	if saved.Model != "IPC2124LB-ADF28KM-H" || saved.MachineName != "IPC2124LB-ADF28KM-H" {
		t.Fatalf("UNV metadata was not saved: %+v", saved)
	}
	if saved.Name != "" {
		t.Fatalf("device name leaked into user camera name: %q", saved.Name)
	}
	if saved.MAC != "88:26:3f:7e:7d:da" {
		t.Fatalf("MAC was not normalized: %q", saved.MAC)
	}
}

func TestStreamSourcesOnlyReturnsUsedCamerasWithInternalCredentials(t *testing.T) {
	service, err := NewService(t.TempDir(), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service.cameras["used"] = persistedCamera{ID: "used", Name: "Front", Use: true, Username: "admin", Password: "p@ss", MainStreamPath: "rtsp://192.168.1.20/main", SubStreamPath: "rtsp://192.168.1.20/sub"}
	service.cameras["unused"] = persistedCamera{ID: "unused", Name: "Back", Use: false, MainStreamPath: "rtsp://192.168.1.21/main"}

	sources := service.StreamSources()
	if len(sources) != 2 {
		t.Fatalf("sources = %+v", sources)
	}
	if sources[0].URL != "rtsp://admin:p%40ss@192.168.1.20/main" && sources[1].URL != "rtsp://admin:p%40ss@192.168.1.20/main" {
		t.Fatalf("credential-bearing main source is missing: %+v", sources)
	}
	for _, source := range sources {
		if source.Name != "Front" || (source.Detail != "Main stream" && source.Detail != "Sub stream") {
			t.Fatalf("source display metadata = %+v", source)
		}
	}
}

func TestOldPersistedCameraHasUnknownInitializationStatus(t *testing.T) {
	camera := cameraFromPersisted(persistedCamera{ID: "old", Address: "192.168.1.20"}, false)
	if camera.InitializationStatus != InitializationUnknown {
		t.Fatalf("initialization status = %q, want %q", camera.InitializationStatus, InitializationUnknown)
	}
}

func TestServiceBlocksSettingsForUninitializedDahua(t *testing.T) {
	service, err := NewService(t.TempDir(), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service.cameras["camera"] = persistedCamera{ID: "camera", Manufacturer: "Dahua", InitializationStatus: InitializationRequired}

	err = service.SaveWithCameraUpdate(context.Background(), SaveRequest{ID: "camera", Address: "192.168.1.20"})
	if !errors.Is(err, ErrDahuaInitializationRequired) {
		t.Fatalf("SaveWithCameraUpdate() error = %v", err)
	}
}

func TestValidateIPv4Network(t *testing.T) {
	tests := []struct {
		name                     string
		address, subnet, gateway string
		wantError                bool
	}{
		{name: "valid", address: "192.168.1.40", subnet: "255.255.255.0", gateway: "192.168.1.1"},
		{name: "invalid address", address: "camera.local", subnet: "255.255.255.0", gateway: "192.168.1.1", wantError: true},
		{name: "non-contiguous mask", address: "192.168.1.40", subnet: "255.0.255.0", gateway: "192.168.1.1", wantError: true},
		{name: "invalid gateway", address: "192.168.1.40", subnet: "255.255.255.0", gateway: "router.local", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateIPv4Network(tt.address, tt.subnet, tt.gateway)
			if (err != nil) != tt.wantError {
				t.Fatalf("validateIPv4Network() error = %v, wantError %t", err, tt.wantError)
			}
		})
	}
}

func TestServiceRefreshVideoStreamsPersistsDahuaParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="camera", nonce="nonce", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("table.Encode[0].MainFormat[0].Video.resolution=1920x1080\n" +
			"table.Encode[0].MainFormat[0].Video.FPS=25\n" +
			"table.Encode[0].ExtraFormat[0].Video.resolution=640x480\n" +
			"table.Encode[0].ExtraFormat[0].Video.FPS=15"))
	}))
	defer server.Close()
	serverURL, _ := url.Parse(server.URL)

	service, err := NewService(t.TempDir(), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service.cameras["camera"] = persistedCamera{
		ID: "camera", Address: serverURL.Hostname(), HTTPPort: uint16Port(t, serverURL.Port()),
		Manufacturer: "Dahua", Username: "admin", Password: "secret",
		InitializationStatus: InitializationCompleted,
	}
	service.dahuaCGI.http = server.Client()

	camera, err := service.RefreshVideoStreams(context.Background(), "camera")
	if err != nil {
		t.Fatal(err)
	}
	if camera.MainStream.Resolution != "1920x1080" || camera.MainStream.FPS != "25" || camera.SubStream.Resolution != "640x480" || camera.SubStream.FPS != "15" {
		t.Fatalf("camera streams = %#v, %#v", camera.MainStream, camera.SubStream)
	}
	reloaded, err := NewService(filepath.Dir(service.filePath), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.List()[0]; got.MainStream != camera.MainStream || got.SubStream != camera.SubStream {
		t.Fatalf("reloaded streams = %#v, %#v", got.MainStream, got.SubStream)
	}
}
