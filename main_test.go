package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"DronService/internal/mediamtx"
	"DronService/internal/stream"
)

func TestMonitorInterval(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "empty uses existing default", value: "", want: 5 * time.Second},
		{name: "valid duration", value: "30s", want: 30 * time.Second},
		{name: "invalid uses existing default", value: "not-a-duration", want: 5 * time.Second},
		{name: "zero uses existing default", value: "0s", want: 5 * time.Second},
		{name: "negative uses existing default", value: "-1s", want: 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := monitorInterval(tt.value); got != tt.want {
				t.Fatalf("monitorInterval(%q) = %s, want %s", tt.value, got, tt.want)
			}
		})
	}
}

func TestIPCamerasPageKeepsCredentialsInRTSPPaths(t *testing.T) {
	required := []string{
		`<input id="password" type="text"`,
		`passwordInput.value=camera.password||''`,
		`parsed.username=usernameInput.value`,
		`parsed.password=passwordInput.value`,
		`mainRTSPInput.value=rtspURLWithCredentials(mainRTSPInput.value)`,
		`subRTSPInput.value=rtspURLWithCredentials(subRTSPInput.value)`,
	}
	for _, fragment := range required {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras page does not contain %q", fragment)
		}
	}
	for _, fragment := range []string{`id="main-rtsp-url"`, `id="sub-rtsp-url"`, `URL для MediaMTX`} {
		if strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras page contains redundant field %q", fragment)
		}
	}
}

func TestIPCamerasDialogShowsDeviceInfoAndMediaMTXToggleBesideName(t *testing.T) {
	required := []string{
		`<h2>Настройки IP-камеры</h2><p id="camera-device-info"`,
		`<div class="camera-name-row"><div class="camera-name-field"><label>Имя</label><input id="name"`,
		`<label class="media-toggle-label"><span>Использовать в MediaMTX</span><input id="use-camera" type="checkbox"></label>`,
		`deviceInfo.textContent=manufacturer+' · '+model`,
		`manufacturer:camera.manufacturer,model:camera.model`,
	}
	for _, fragment := range required {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras dialog does not contain %q", fragment)
		}
	}
	for _, fragment := range []string{`<select id="manufacturer">`, `<input id="model">`} {
		if strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras dialog still contains editable device field %q", fragment)
		}
	}
}

func TestIPCamerasHeadingPlacesDiscoveryButtonOnTheRight(t *testing.T) {
	if !strings.Contains(ipCamerasPageHTML, `<div class="page-heading"><h1>Список IP-камер</h1><button id="refresh-cameras">Найти камеры</button></div>`) {
		t.Fatal("IP cameras heading and discovery button are not in one row")
	}
	for _, fragment := range []string{`.page-heading{display:flex`, `justify-content:space-between`} {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras page does not contain %q", fragment)
		}
	}
}

func TestIPCamerasPageSupportsDahuaInitializationAndNetworkSettings(t *testing.T) {
	for _, fragment := range []string{
		`<th>Инициализация</th>`,
		`eq .InitializationStatus "uninitialized"`,
		`>Авторизовать</a>`,
		`value.initializationStatus==='uninitialized'`,
		`id="subnet-mask"`,
		`id="gateway"`,
		`id="video-stream-info"`,
		`/video-streams',{method:'POST'}`,
		`streamDescription('Основной поток'`,
		`streamDescription('Дополнительный поток'`,
	} {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras page does not contain %q", fragment)
		}
	}
}

func TestStreamsHeadingPlacesAddButtonOnTheRight(t *testing.T) {
	if !strings.Contains(streamsPageHTML, `<div class="page-heading"><h1>MediaMTX`) {
		t.Fatal("MediaMTX page heading is missing")
	}
	if !strings.Contains(streamsPageHTML, `<button id="add" {{if not .Sources}}disabled{{end}}>Добавить стрим</button>`) {
		t.Fatal("add stream button is not in the MediaMTX heading")
	}
	for _, fragment := range []string{`.page-heading{display:flex`, `justify-content:space-between`} {
		if !strings.Contains(streamsPageHTML, fragment) {
			t.Errorf("streams page does not contain %q", fragment)
		}
	}
}

func TestApplicationPagesShowRaspberryNetworkAddresses(t *testing.T) {
	pages := map[string]string{
		"devices":    devicesPageHTML,
		"ip-cameras": ipCamerasPageHTML,
		"streams":    streamsPageHTML,
		"zerotier":   zeroTierPageHTML,
	}
	for name, page := range pages {
		t.Run(name, func(t *testing.T) {
			for _, fragment := range []string{
				`id="network-info"`,
				`fetch('/api/system/network')`,
				`'LAN: '+(s.lan?.join(', ')||'—')`,
				`'Wi-Fi: '+(s.wifi?.join(', ')||'—')`,
				`if(s.localName)parts.push('Имя: '+s.localName)`,
			} {
				if !strings.Contains(page, fragment) {
					t.Errorf("page does not contain %q", fragment)
				}
			}
		})
	}
}

func TestApplicationPagesShowVersionAndManualUpdateButton(t *testing.T) {
	pages := map[string]string{
		"devices":    devicesPageHTML,
		"ip-cameras": ipCamerasPageHTML,
		"streams":    streamsPageHTML,
		"zerotier":   zeroTierPageHTML,
	}
	for name, page := range pages {
		t.Run(name, func(t *testing.T) {
			for _, fragment := range []string{
				`id="app-version"`,
				`id="update-app"`,
				`id="update-app-state"`,
				`src="/assets/application-status.js" defer`,
			} {
				if !strings.Contains(page, fragment) {
					t.Errorf("page does not contain %q", fragment)
				}
			}
		})
	}
}

func TestStreamNameAllowsOnlyLatinIdentifierCharacters(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{name: "camera", valid: true},
		{name: "Camera_01", valid: true},
		{name: "front-camera", valid: true},
		{name: "front camera", valid: false},
		{name: "камера", valid: false},
		{name: "front.camera", valid: false},
		{name: "", valid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := streamNamePattern.MatchString(tt.name); got != tt.valid {
				t.Fatalf("stream name %q validity = %t, want %t", tt.name, got, tt.valid)
			}
		})
	}
}

func TestStreamsDialogExplainsStreamNameRestriction(t *testing.T) {
	required := []string{
		`id="stream-name-info"`,
		`id="stream-name-info-dialog"`,
		`aria-label="Ограничения имени стрима"`,
		`только латинские буквы`,
		`Пробелы недопустимы`,
		`streamNameInfoDialog.showModal()`,
	}
	for _, fragment := range required {
		if !strings.Contains(streamsPageHTML, fragment) {
			t.Errorf("streams dialog does not contain %q", fragment)
		}
	}
}

func TestStreamsPageUsesEditableNamesIconsCodecsAndIPAddresses(t *testing.T) {
	for _, fragment := range []string{
		`nameInput.disabled=false`,
		`originalName:originalName`,
		`class="source-icon"`,
		`class="source-detail"`,
		`H.264`,
		`Proxy`,
	} {
		if !strings.Contains(streamsPageHTML, fragment) {
			t.Errorf("streams page does not contain %q", fragment)
		}
	}
	if strings.Contains(streamsPageHTML, `nameInput.disabled=editing`) {
		t.Fatal("stream name is still disabled while editing")
	}

	tests := []struct {
		name   string
		status networkStatusResponse
		want   string
	}{
		{name: "LAN preferred", status: networkStatusResponse{LAN: []string{"192.168.1.20"}, WiFi: []string{"192.168.1.30"}}, want: "192.168.1.20"},
		{name: "Wi-Fi fallback", status: networkStatusResponse{WiFi: []string{"192.168.1.30"}}, want: "192.168.1.30"},
		{name: "loopback fallback", status: networkStatusResponse{}, want: "127.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := preferredRTSPHostFromStatus(tt.status); got != tt.want {
				t.Fatalf("preferredRTSPHostFromStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAnalogCameraDialogUsesMediaMTXToggleBesideName(t *testing.T) {
	fragment := `<div class="camera-name-row"><div class="camera-name-field"><label for="camera-name">Имя камеры</label><input id="camera-name" maxlength="100" required></div><label class="media-toggle-label"><span>Использовать в MediaMTX</span><input id="use-camera" type="checkbox"></label></div>`
	if !strings.Contains(devicesPageHTML, fragment) {
		t.Fatal("analog camera dialog does not use the IP-camera toggle layout")
	}
}

func TestStreamsHandlerReturnsDronServiceModel(t *testing.T) {
	mediaMTX := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"name":"camera1","ready":false,"online":false,"readers":[]}]}`))
	}))
	defer mediaMTX.Close()

	service := stream.NewService(mediamtx.NewClient(mediaMTX.URL, "", ""))
	request := httptest.NewRequest(http.MethodGet, "/api/streams", nil)
	response := httptest.NewRecorder()

	streamsHandler(service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"state":"idle"`) {
		t.Fatalf("response does not contain DronService state: %s", body)
	}
	if strings.Contains(body, `"ready"`) || strings.Contains(body, `"online"`) {
		t.Fatalf("response exposes MediaMTX fields: %s", body)
	}
}

func TestStreamsHandlerDoesNotExposeUpstreamError(t *testing.T) {
	mediaMTX := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal upstream detail", http.StatusInternalServerError)
	}))
	defer mediaMTX.Close()

	service := stream.NewService(mediamtx.NewClient(mediaMTX.URL, "", ""))
	request := httptest.NewRequest(http.MethodGet, "/api/streams", nil)
	response := httptest.NewRecorder()

	streamsHandler(service).ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	if strings.Contains(response.Body.String(), "internal upstream detail") {
		t.Fatalf("response exposes upstream error: %s", response.Body.String())
	}
}
