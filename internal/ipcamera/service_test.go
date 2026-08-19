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
	"strings"
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

func TestDiscoveryEvidenceFillsMissingInterfaceName(t *testing.T) {
	saved := persistedCamera{ID: "mac:e0:2e:fe:6a:6c:27", Address: "192.168.1.108"}
	updated := updateGenericDiscoveredCamera(saved, saved.ID, DiscoveredDevice{Vendor: "Dahua", MAC: "e0:2e:fe:6a:6c:27", IP: net.ParseIP("192.168.1.108"), InterfaceName: "eth0"}, time.Now())
	if updated.InterfaceName != "eth0" {
		t.Fatalf("InterfaceName=%q", updated.InterfaceName)
	}
	view := cameraFromPersisted(updated, true)
	if view.InterfaceName != "eth0" {
		t.Fatalf("camera InterfaceName=%q", view.InterfaceName)
	}
}

func TestServiceDeletePersistsRemoval(t *testing.T) {
	directory := t.TempDir()
	service, err := NewService(directory, DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request := SaveRequest{ID: "camera", Name: "Camera", Address: "192.168.1.20", Manufacturer: "Dahua", MainStreamPath: "rtsp://192.168.1.20/main", SubStreamPath: "rtsp://192.168.1.20/sub"}
	if err := service.Save(request); err != nil {
		t.Fatal(err)
	}
	service.online[request.ID] = true
	if err := service.Delete(request.ID); err != nil {
		t.Fatal(err)
	}
	if len(service.List()) != 0 || service.online[request.ID] {
		t.Fatal("deleted camera remains in service state")
	}
	reloaded, err := NewService(directory, DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.List()) != 0 {
		t.Fatal("deleted camera remains on disk")
	}
	if err := service.Delete(request.ID); !errors.Is(err, ErrCameraNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrCameraNotFound", err)
	}
}

func TestCameraViewRedactsPasswordAndRTSPCredentials(t *testing.T) {
	saved := persistedCamera{
		ID:             "camera",
		Address:        "192.168.1.20",
		Username:       "admin",
		Password:       "p@ss word",
		MainStreamPath: "rtsp://legacy:leaked@192.168.1.20/main",
		SubStreamPath:  "rtsps://legacy:leaked@192.168.1.20/sub",
	}

	camera := cameraFromPersisted(saved, true)
	if !camera.HasPassword {
		t.Fatal("HasPassword = false, want true")
	}
	if camera.MainStreamPath != "rtsp://192.168.1.20/main" {
		t.Fatalf("MainStreamPath = %q", camera.MainStreamPath)
	}
	if camera.SubStreamPath != "rtsps://192.168.1.20/sub" {
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
	if _, exists := response["password"]; exists {
		t.Fatalf("camera response exposes password: %#v", response)
	}
	if response["hasPassword"] != true || response["mainStreamPath"] != camera.MainStreamPath || response["subStreamPath"] != camera.SubStreamPath {
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

func TestServiceSaveRebuildsRTSPHostsFromCameraAddress(t *testing.T) {
	service, err := NewService(t.TempDir(), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request := SaveRequest{
		ID: "camera", Name: "Camera", Address: "192.168.1.40", Manufacturer: "Dahua",
		Username: "admin", Password: "secret",
		MainStreamPath: "rtsp://admin:secret@192.168.1.20:8554/cam/realmonitor?channel=1&subtype=0",
		SubStreamPath:  "rtsp://admin:secret@192.168.1.20:8554/cam/realmonitor?channel=1&subtype=1",
	}
	if err := service.Save(request); err != nil {
		t.Fatal(err)
	}

	saved := service.cameras[request.ID]
	if saved.MainStreamPath != "rtsp://192.168.1.40:8554/cam/realmonitor?channel=1&subtype=0" {
		t.Fatalf("MainStreamPath = %q", saved.MainStreamPath)
	}
	if saved.SubStreamPath != "rtsp://192.168.1.40:8554/cam/realmonitor?channel=1&subtype=1" {
		t.Fatalf("SubStreamPath = %q", saved.SubStreamPath)
	}
}

func TestDiscoveryRebuildsRTSPHostsWhenCameraAddressChanges(t *testing.T) {
	saved := persistedCamera{
		MainStreamPath:       "rtsp://192.168.1.20:8554/custom/main?profile=1",
		SubStreamPath:        "rtsps://192.168.1.20:8322/custom/sub?profile=2",
		InitializationStatus: InitializationCompleted,
	}
	updated := updateGenericDiscoveredCamera(saved, "camera", DiscoveredDevice{
		Vendor: "UNV", Manufacturer: "UNV", IP: net.ParseIP("192.168.1.40"),
	}, time.Now())

	if updated.MainStreamPath != "rtsp://192.168.1.40:8554/custom/main?profile=1" {
		t.Fatalf("MainStreamPath = %q", updated.MainStreamPath)
	}
	if updated.SubStreamPath != "rtsps://192.168.1.40:8322/custom/sub?profile=2" {
		t.Fatalf("SubStreamPath = %q", updated.SubStreamPath)
	}
	if updated.InitializationStatus != InitializationCompleted {
		t.Fatalf("InitializationStatus = %q, want %q", updated.InitializationStatus, InitializationCompleted)
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
	service.cameras["used"] = persistedCamera{
		ID: "used", Name: "Front", Use: true, Username: "admin", Password: "p@ss",
		MainStreamPath: "rtsp://192.168.1.20/main", SubStreamPath: "rtsp://192.168.1.20/sub",
		MainStream: VideoStream{Resolution: "1920x1080", FPS: "25", BitrateKbps: 2048},
		SubStream:  VideoStream{Resolution: "704x576", FPS: "15", BitrateKbps: 512},
	}
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
		switch source.Kind {
		case "main":
			if source.Metadata != (VideoStream{Resolution: "1920x1080", FPS: "25", BitrateKbps: 2048}) {
				t.Fatalf("main source metadata = %+v", source.Metadata)
			}
		case "sub":
			if source.Metadata != (VideoStream{Resolution: "704x576", FPS: "15", BitrateKbps: 512}) {
				t.Fatalf("sub source metadata = %+v", source.Metadata)
			}
		default:
			t.Fatalf("source kind = %q", source.Kind)
		}
	}
}

func TestPreviewStreamSource(t *testing.T) {
	service, err := NewService(t.TempDir(), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service.cameras["configured"] = persistedCamera{
		ID: "configured", Name: "Front", Address: "192.168.1.20",
		Username: "admin", Password: "p@ss word", MainStreamPath: "rtsp://192.168.1.20/main", SubStreamPath: "rtsp://192.168.1.20/sub",
		InitializationStatus: InitializationCompleted,
		MainStream:           VideoStream{Resolution: "1920x1080", FPS: "25", BitrateKbps: 2048},
		SubStream:            VideoStream{Resolution: "704x576", FPS: "25", BitrateKbps: 512},
	}
	service.cameras["legacy"] = persistedCamera{
		ID: "legacy", Address: "192.168.1.21", LegacyRTSPPath: "rtsp://192.168.1.21/legacy",
	}
	service.cameras["uninitialized"] = persistedCamera{
		ID: "uninitialized", MainStreamPath: "rtsp://192.168.1.22/main",
		InitializationStatus: InitializationRequired,
	}
	service.cameras["invalid"] = persistedCamera{ID: "invalid", MainStreamPath: "http://192.168.1.23/main"}

	source, err := service.PreviewStreamSource("configured", "main")
	if err != nil {
		t.Fatal(err)
	}
	want := StreamSource{
		ID: "configured:main", Name: "Front", Detail: "Main stream", Kind: "main",
		Metadata: VideoStream{Resolution: "1920x1080", FPS: "25", BitrateKbps: 2048},
		URL:      "rtsp://admin:p%40ss%20word@192.168.1.20/main",
	}
	if source != want {
		t.Fatalf("PreviewStreamSource(main) = %+v, want %+v", source, want)
	}
	sub, err := service.PreviewStreamSource("configured", "sub")
	if err != nil {
		t.Fatal(err)
	}
	wantSub := StreamSource{
		ID: "configured:sub", Name: "Front", Detail: "Sub stream", Kind: "sub",
		Metadata: VideoStream{Resolution: "704x576", FPS: "25", BitrateKbps: 512},
		URL:      "rtsp://admin:p%40ss%20word@192.168.1.20/sub",
	}
	if sub != wantSub {
		t.Fatalf("PreviewStreamSource(sub) = %+v, want %+v", sub, wantSub)
	}

	legacy, err := service.PreviewStreamSource("legacy", "main")
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Name != "192.168.1.21" || legacy.URL != "rtsp://192.168.1.21/legacy" {
		t.Fatalf("legacy PreviewStreamSource(main) = %+v", legacy)
	}
	if _, err := service.PreviewStreamSource("legacy", "sub"); err == nil {
		t.Fatal("legacy PreviewStreamSource(sub) error = nil")
	}

	if _, err := service.PreviewStreamSource("missing", "main"); !errors.Is(err, ErrCameraNotFound) {
		t.Fatalf("missing PreviewStreamSource() error = %v", err)
	}
	if _, err := service.PreviewStreamSource("uninitialized", "main"); !errors.Is(err, ErrDahuaInitializationRequired) {
		t.Fatalf("uninitialized PreviewStreamSource() error = %v", err)
	}
	if _, err := service.PreviewStreamSource("invalid", "main"); err == nil {
		t.Fatal("invalid PreviewStreamSource() error = nil")
	}
	if _, err := service.PreviewStreamSource("configured", "third"); !errors.Is(err, ErrInvalidPreviewStream) {
		t.Fatalf("unsupported PreviewStreamSource() error = %v", err)
	}
}

func TestOldPersistedCameraHasUnknownInitializationStatus(t *testing.T) {
	camera := cameraFromPersisted(persistedCamera{ID: "old", Address: "192.168.1.20"}, false)
	if camera.InitializationStatus != InitializationUnknown {
		t.Fatalf("initialization status = %q, want %q", camera.InitializationStatus, InitializationUnknown)
	}
}

func TestInitializationAfterDiscovery(t *testing.T) {
	tests := []struct {
		name               string
		previous, observed InitializationStatus
		want               InitializationStatus
	}{
		{name: "confirmed remains initialized when discovery is unknown", previous: InitializationCompleted, observed: InitializationUnknown, want: InitializationCompleted},
		{name: "uninitialized becomes unknown after inconclusive discovery", previous: InitializationRequired, observed: InitializationUnknown, want: InitializationUnknown},
		{name: "explicit initialized replaces uninitialized", previous: InitializationRequired, observed: InitializationCompleted, want: InitializationCompleted},
		{name: "explicit uninitialized replaces initialized", previous: InitializationCompleted, observed: InitializationRequired, want: InitializationRequired},
		{name: "unknown remains unknown", previous: InitializationUnknown, observed: InitializationUnknown, want: InitializationUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := initializationAfterDiscovery(tt.previous, tt.observed); got != tt.want {
				t.Fatalf("initializationAfterDiscovery(%q, %q) = %q, want %q", tt.previous, tt.observed, got, tt.want)
			}
		})
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
			"table.Encode[0].MainFormat[0].Video.BitRate=4096\n" +
			"table.Encode[0].ExtraFormat[0].Video.resolution=640x480\n" +
			"table.Encode[0].ExtraFormat[0].Video.FPS=15\n" +
			"table.Encode[0].ExtraFormat[0].Video.BitRate=512"))
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
		InitializationStatus: InitializationUnknown,
	}
	service.dahuaCGI.http = server.Client()

	camera, err := service.RefreshVideoStreams(context.Background(), "camera")
	if err != nil {
		t.Fatal(err)
	}
	if camera.MainStream != (VideoStream{Resolution: "1920x1080", FPS: "25", BitrateKbps: 4096}) || camera.SubStream != (VideoStream{Resolution: "640x480", FPS: "15", BitrateKbps: 512}) {
		t.Fatalf("camera streams = %#v, %#v", camera.MainStream, camera.SubStream)
	}
	if camera.InitializationStatus != InitializationCompleted {
		t.Fatalf("camera initialization status = %q, want %q", camera.InitializationStatus, InitializationCompleted)
	}
	reloaded, err := NewService(filepath.Dir(service.filePath), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.List()[0]; got.MainStream != camera.MainStream || got.SubStream != camera.SubStream || got.InitializationStatus != InitializationCompleted {
		t.Fatalf("reloaded camera = %#v", got)
	}
}

func TestCheckKnownUNVStatusUsesReachableTCPService(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	address, port, _ := net.SplitHostPort(listener.Addr().String())
	targets := map[string]unvStatusTarget{
		"online":  {Address: address, HTTPPort: uint16Port(t, port)},
		"offline": {Address: "127.0.0.1", HTTPPort: 1},
	}

	online := checkKnownUNVStatus(context.Background(), targets, 100*time.Millisecond)
	if len(online) != 1 || online[0] != "online" {
		t.Fatalf("online IDs = %#v, want [online]", online)
	}
}

func TestServiceRefreshVideoStreamsPersistsUNVParameters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="camera", nonce="nonce", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/LAPI/V1.0/System/DeviceInfo":
			_, _ = w.Write([]byte(`{"Response":{"ResponseCode":0,"Data":{
				"DeviceName":"Front camera",
				"DeviceModel":"IPC2124LE-ADF28KM-H",
				"SerialNumber":"serial",
				"FirmwareVersion":"firmware"
			}}}`))
		case "/LAPI/V1.0/Channels/0/Media/Video/Streams/DetailInfos":
			_, _ = w.Write([]byte(`{"Response":{"ResponseCode":0,"Data":{
				"Num": 2,
				"VideoStreamInfos": [{
					"ID": 0,
					"VideoEncodeInfo": {
						"Resolution": {"Width": 1920, "Height": 1080},
						"FrameRate": 25,
						"BitRate": 4096
					}
				}, {
					"ID": 1,
					"VideoEncodeInfo": {
						"Resolution": {"Width": 640, "Height": 360},
						"FrameRate": 12,
						"BitRate": 768
					}
				}]
			}}}`))
		case "/LAPI/V1.0/Channels/0/Media/Video/Streams/0", "/LAPI/V1.0/Channels/0/Media/Video/Streams/1":
			_, _ = w.Write([]byte(`{"Response":{"ResponseCode":4,"ResponseString":"Not Supported","Data":"null"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL, _ := url.Parse(server.URL)

	service, err := NewService(t.TempDir(), DahuaDiscoverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	service.cameras["camera"] = persistedCamera{
		ID:             "camera",
		Address:        serverURL.Hostname(),
		HTTPPort:       uint16Port(t, serverURL.Port()),
		Manufacturer:   "UNV",
		Protocol:       "UNV-UDP-3702",
		Username:       "admin",
		Password:       "secret",
		MainStreamPath: "rtsp://127.0.0.1/main",
		SubStreamPath:  "rtsp://127.0.0.1/sub",
	}
	service.unvLAPI.digest.http = server.Client()
	service.rtspProbe = func(_ context.Context, source string) (VideoStream, error) {
		if strings.Contains(source, "/main") {
			return VideoStream{Resolution: "1280x720", FPS: "12"}, nil
		}
		return VideoStream{Resolution: "640x360", FPS: "6"}, nil
	}

	camera, err := service.RefreshVideoStreams(context.Background(), "camera")
	if err != nil {
		t.Fatal(err)
	}
	if camera.Model != "IPC2124LE-ADF28KM-H" || camera.Serial != "serial" || camera.Firmware != "firmware" {
		t.Fatalf("camera identity = %#v", camera)
	}
	if camera.MainStream != (VideoStream{Resolution: "1920x1080", FPS: "25", BitrateKbps: 4096}) {
		t.Fatalf("main stream = %#v", camera.MainStream)
	}
	if camera.SubStream != (VideoStream{Resolution: "640x360", FPS: "12", BitrateKbps: 768}) {
		t.Fatalf("sub stream = %#v", camera.SubStream)
	}
	if camera.InitializationStatus != InitializationCompleted {
		t.Fatalf("initialization status = %q", camera.InitializationStatus)
	}
}
