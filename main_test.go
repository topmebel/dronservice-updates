package main

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"DronService/internal/mediamtx"
	"DronService/internal/stream"
	"DronService/internal/v4l2"
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

func TestShutdownWaitsForTemporaryAccessCleanup(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	value := string(source)
	for _, fragment := range []string{
		"shutdownDone := make(chan struct{})",
		"defer close(shutdownDone)",
		"cameraProxyManager.Close(cleanupCtx)",
		"streamPreviewManager.Close(cleanupCtx)",
		"<-shutdownDone",
	} {
		if !strings.Contains(value, fragment) {
			t.Errorf("shutdown flow does not contain %q", fragment)
		}
	}
}

func TestIPCamerasPageKeepsCredentialsOutOfPublicRTSPPaths(t *testing.T) {
	required := []string{
		`<input id="password" type="password" autocomplete="new-password">`,
		`passwordInput.value=''`,
		`passwordInput.placeholder=camera.hasPassword?'Пароль сохранён — оставьте пустым, чтобы не менять':'Введите пароль'`,
		`addressInput=document.querySelector('#address')`,
		`parsed.hostname=address`,
		`parsed.username=''`,
		`parsed.password=''`,
		`mainRTSPInput.value=rtspURLFromInputs(mainRTSPInput.value)`,
		`subRTSPInput.value=rtspURLFromInputs(subRTSPInput.value)`,
		`addressInput.addEventListener('input',refreshRTSPPaths)`,
		`cameraPayload(){refreshRTSPPaths()`,
	}
	for _, fragment := range required {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras page does not contain %q", fragment)
		}
	}
	for _, fragment := range []string{
		`id="main-rtsp-url"`,
		`id="sub-rtsp-url"`,
		`URL для MediaMTX`,
		`passwordInput.value=camera.password`,
		`parsed.username=usernameInput.value`,
		`parsed.password=passwordInput.value`,
	} {
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

func TestIPCamerasDialogPlacesCredentialsInOneRowWithoutStreamMetadata(t *testing.T) {
	required := []string{
		`<div class="credentials-fields"><div class="credential-field"><label for="username">Логин</label><input id="username"`,
		`</div><div class="credential-field"><label for="password">Пароль</label><input id="password"`,
		`.camera-name-row,.network-fields,.credentials-fields{display:flex`,
		`.camera-name-row,.network-fields,.credentials-fields{align-items:stretch;flex-direction:column`,
	}
	for _, fragment := range required {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras dialog does not contain %q", fragment)
		}
	}
	for _, fragment := range []string{
		`id="video-stream-info"`,
		`videoStreamInfo`,
		`renderVideoStreams`,
		`refreshVideoStreams`,
	} {
		if strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras dialog still contains stream metadata UI %q", fragment)
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
		`}}Авторизовать{{else if`,
		`data-camera-access="{{.ID}}"`,
		`/setup-access',{method:'POST'}`,
		`value.initializationStatus==='uninitialized'`,
		`id="subnet-mask"`,
		`id="gateway"`,
		`/video-streams',{method:'POST'}`,
		`Инициализирована · Открыть ↗`,
		`Неизвестно · Открыть ↗`,
	} {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras page does not contain %q", fragment)
		}
	}
}

func TestIPCamerasPageShowsStreamMetadataAndTemporaryHLSPreview(t *testing.T) {
	required := []string{
		`<thead><tr><th>Имя</th><th>IP-адрес</th><th>Производитель</th><th>Модель</th><th>Инициализация</th><th class="toggle-cell">MediaMTX</th><th class="status-cell">Состояние</th></tr></thead>`,
		`<tr class="camera-row" data-camera=`,
		`<tr class="stream-details-row" data-stream-details="{{.ID}}">`,
		`<td colspan="7"><div class="stream-details">`,
		`<strong>Main:</strong><span data-main-stream>`,
		`<strong>Sub:</strong><span data-sub-stream>`,
		`data-camera-preview="{{.ID}}" data-preview-stream="main"`,
		`data-camera-preview="{{.ID}}" data-preview-stream="sub"`,
		`cameraStreamDetailsRow(row,value.id)`,
		`const details=row.nextElementSibling`,
		`videoStreamsLoaded.has(value.id)`,
		`finally{videoStreamRequests.delete(value.id)}`,
		`.MainStream.BitrateKbps`,
		`.SubStream.BitrateKbps`,
		`<dialog id="camera-preview-dialog"`,
		`id="camera-preview-frame"`,
		`result.url`,
		`button.closest('tr[data-stream-details]')`,
		`details?.previousElementSibling`,
		`streamLabel=stream==='sub'?'Sub stream':'Main stream'`,
		`+'/preview?stream='+encodeURIComponent(stream),{method:'POST'}`,
		`'/preview/'+encodeURIComponent(session.sessionID)`,
		`{method:'DELETE',keepalive:true}`,
		`previewFrame.src='about:blank'`,
		`previewDialog.addEventListener('close'`,
		`const videoStreamRequests=new Map(),videoStreamsLoaded=new Set()`,
		`!value.hasPassword`,
		`if(cameraStatusRefreshed)loadTableVideoStreams()`,
	}
	for _, fragment := range required {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras preview table does not contain %q", fragment)
		}
	}
	for _, fragment := range []string{
		`<th>Main stream</th>`,
		`<th>Sub stream</th>`,
		`<th class="preview-cell">Просмотр</th>`,
		`<td class="preview-cell"><button`,
		`row=button.closest('tr[data-camera]')`,
	} {
		if strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras table still contains obsolete preview or stream column %q", fragment)
		}
	}
	if got := strings.Count(ipCamerasPageHTML, `data-camera-preview="{{.ID}}"`); got != 2 {
		t.Errorf("IP cameras table contains %d preview buttons, want 2", got)
	}
	for _, fragment := range []string{`<th>MAC</th>`, `<td>{{.MAC}}</td>`} {
		if strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP cameras table still displays MAC address through %q", fragment)
		}
	}
}

func TestIPCamerasPreviewModalShowsSelectedStreamMetadata(t *testing.T) {
	required := []string{
		`aria-describedby="camera-preview-metadata"`,
		`id="camera-preview-metadata" class="preview-metadata" aria-live="polite"`,
		`id="camera-preview-status" class="preview-status" role="status" aria-live="polite"`,
		`previewMetadata=document.querySelector('#camera-preview-metadata')`,
		`details?.querySelector(kind==='sub'?'[data-sub-stream]':'[data-main-stream]')?.textContent?.trim()`,
		`previewMetadata.textContent='Поток: '+previewStreamLabel(kind)+' · '+current`,
		`previewMetadata.textContent='Поток: '+previewStreamLabel(kind)+' · Разрешение: '`,
		`value?.kind==='sub'`,
		`value?.resolution||'—'`,
		`value?.fps||'—'`,
		`value?.bitrateKbps?value.bitrateKbps+' кбит/с':'—'`,
		`renderCameraPreviewFallback(details,stream)`,
		`renderCameraPreviewMetadata(result.stream,stream)`,
		`previewMetadata.textContent=''`,
	}
	for _, fragment := range required {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP camera preview metadata does not contain %q", fragment)
		}
	}
}

func TestIPCamerasBackgroundStatusRefreshDoesNotInterruptOpenDialog(t *testing.T) {
	for _, fragment := range []string{
		`if(dialog.open||previewDialog.open){history.replaceState(null,'','/ip-cameras?refreshed=1');notice.hidden=true;return}`,
		`setInterval(refreshIPCameras,5000);if(cameraStatusRefreshed)loadTableVideoStreams();else checkCameraStatus()`,
	} {
		if !strings.Contains(ipCamerasPageHTML, fragment) {
			t.Errorf("IP camera background refresh does not contain %q", fragment)
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

func TestStreamsPageShowsExistingPathInHLSPreviewModal(t *testing.T) {
	required := []string{
		`data-hls-base="{{.PublicHLSBase}}"`,
		`data-stream-preview="{{.Name}}"`,
		`>Просмотр ▶</button>`,
		`<dialog id="stream-preview-dialog"`,
		`aria-describedby="stream-preview-metadata"`,
		`id="stream-preview-metadata" class="preview-metadata" aria-live="polite"`,
		`id="stream-preview-frame"`,
		`previewMetadata.textContent='Разрешение: '+resolution+' · FPS: '+fps+' · Битрейт: '+bitrate`,
		`hlsBase.replace(/\/+$/,'')+'/'+encodeURIComponent(name)`,
		`previewFrame.src='about:blank'`,
		`previewMetadata.textContent=''`,
		`previewDialog.addEventListener('close'`,
		`if(event.target.closest('button'))return`,
	}
	for _, fragment := range required {
		if !strings.Contains(streamsPageHTML, fragment) {
			t.Errorf("streams preview table does not contain %q", fragment)
		}
	}
}

func TestStreamsPageShowsStreamSettingsInSecondRow(t *testing.T) {
	required := []string{
		`<tr class="stream-config-row" data-name="{{.Name}}" data-source-id="{{.SourceID}}">`,
		`<tr class="stream-details-row" data-stream-details="{{.Name}}"><td colspan="4">`,
		`<strong>Разрешение:</strong><span data-stream-resolution>{{if .Resolution}}{{.Resolution}}{{else}}—{{end}}</span>`,
		`<strong>FPS:</strong><span data-stream-fps>{{if .FPS}}{{.FPS}}{{else}}—{{end}}</span>`,
		`<strong>Битрейт:</strong><span data-stream-bitrate>{{if .BitrateKbps}}{{.BitrateKbps}} кбит/с{{else if eq .SourceType "analog"}}Динамический (CRF 23){{else}}—{{end}}</span>`,
		`data-stream-bitrate]')?.textContent?.trim()||'—'`,
		`<button type="button" class="preview-button" data-stream-preview="{{.Name}}"`,
		`.stream-config-row td{border-bottom:0!important}`,
		`.stream-details-row:hover{background:#172033!important}`,
		`document.querySelector('#streams-body').replaceChildren(...next.children)`,
		`const row=event.target.closest('tr[data-name]')`,
	}
	if strings.Contains(streamsPageHTML, `<th class="preview-cell">Просмотр</th>`) {
		t.Fatal("MediaMTX preview still has a separate table column")
	}
	for _, fragment := range required {
		if !strings.Contains(streamsPageHTML, fragment) {
			t.Errorf("streams metadata table does not contain %q", fragment)
		}
	}
}

func TestStreamsPageRendersKnownDynamicAndUnknownSettings(t *testing.T) {
	page, err := template.New("streams-metadata-test").Parse(streamsPageHTML)
	if err != nil {
		t.Fatalf("parse streams page: %v", err)
	}
	configs := []streamPageConfig{
		{Config: stream.Config{Name: "front", SourceType: "ip", Resolution: "1920x1080", FPS: "25", BitrateKbps: 4096}},
		{Config: stream.Config{Name: "analog", SourceType: "analog", Resolution: "720x576", FPS: "25"}},
		{Config: stream.Config{Name: "external"}},
	}
	var output strings.Builder
	err = page.Execute(&output, struct {
		Configs        []streamPageConfig
		Sources        []stream.Source
		PublicRTSPBase string
		PublicHLSBase  string
		MediaMTX       mediamtx.InstallStatus
	}{
		Configs:  configs,
		MediaMTX: mediamtx.InstallStatus{Installed: true},
	})
	if err != nil {
		t.Fatalf("render streams page: %v", err)
	}
	rendered := output.String()
	for _, fragment := range []string{
		`<strong>Разрешение:</strong><span data-stream-resolution>1920x1080</span>`,
		`<strong>FPS:</strong><span data-stream-fps>25</span>`,
		`<strong>Битрейт:</strong><span data-stream-bitrate>4096 кбит/с</span>`,
		`<strong>Битрейт:</strong><span data-stream-bitrate>Динамический (CRF 23)</span>`,
		`<strong>Разрешение:</strong><span data-stream-resolution>—</span></span><span class="stream-detail"><strong>FPS:</strong><span data-stream-fps>—</span></span><span class="stream-detail"><strong>Битрейт:</strong><span data-stream-bitrate>—</span>`,
	} {
		if !strings.Contains(rendered, fragment) {
			t.Errorf("rendered streams metadata does not contain %q", fragment)
		}
	}
	if got := strings.Count(rendered, `class="stream-details-row"`); got != len(configs) {
		t.Fatalf("rendered streams details rows = %d, want %d", got, len(configs))
	}
}

func TestValidPublicStreamNameRejectsInternalPreviewPaths(t *testing.T) {
	for _, name := range []string{"camera", "front-camera", "front_camera", stream.InternalPreviewPathPrefix + "operator"} {
		if !validPublicStreamName(name) {
			t.Errorf("validPublicStreamName(%q) = false", name)
		}
	}
	for _, name := range []string{stream.InternalPreviewPathPrefix + "0123456789abcdef01234567", "front camera", ""} {
		if validPublicStreamName(name) {
			t.Errorf("validPublicStreamName(%q) = true", name)
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

func renderDevicesPage(t *testing.T, devices ...v4l2.Device) string {
	t.Helper()
	page, err := template.New("devices-test").Parse(devicesPageHTML)
	if err != nil {
		t.Fatalf("parse devices page: %v", err)
	}
	var output strings.Builder
	if err := page.Execute(&output, struct{ Devices []v4l2.Device }{Devices: devices}); err != nil {
		t.Fatalf("render devices page: %v", err)
	}
	return output.String()
}

func TestAnalogCameraTableShowsConfiguredCaptureInSecondRow(t *testing.T) {
	rendered := renderDevicesPage(t, v4l2.Device{
		ID:                 "usb-camera",
		Path:               "/dev/video2",
		ConfiguredName:     "Задняя камера",
		SelectedFormat:     "YUYV",
		SelectedResolution: "720x576",
		SelectedFPS:        "25",
		Use:                false,
	})

	for _, fragment := range []string{
		`<tr class="camera-row" data-device-path="/dev/video2"`,
		`<tr class="stream-details-row" data-stream-details="usb-camera">`,
		`<td colspan="7"><div class="stream-details">`,
		`<strong>Захват:</strong><span data-device-stream>YUYV · 720x576 · 25 FPS</span>`,
		`data-device-preview="usb-camera"`,
		`>Просмотр ▶</button>`,
	} {
		if !strings.Contains(rendered, fragment) {
			t.Errorf("rendered analog camera table does not contain %q", fragment)
		}
	}

	buttonStart := strings.Index(rendered, `<button type="button" class="camera-link preview-button" data-device-preview="usb-camera"`)
	if buttonStart < 0 {
		t.Fatal("analog preview button is missing")
	}
	buttonEnd := strings.Index(rendered[buttonStart:], `</button>`)
	if buttonEnd < 0 {
		t.Fatal("analog preview button is incomplete")
	}
	if button := rendered[buttonStart : buttonStart+buttonEnd]; strings.Contains(button, "disabled") {
		t.Fatal("analog preview is disabled when MediaMTX Use=false despite a saved capture mode")
	}
}

func TestAnalogCameraTableDisablesPreviewWithoutSavedCaptureMode(t *testing.T) {
	rendered := renderDevicesPage(t, v4l2.Device{ID: "usb-camera", Path: "/dev/video0"})
	if !strings.Contains(rendered, `<span data-device-stream>Режим не настроен</span>`) {
		t.Fatal("unconfigured analog camera does not explain that its capture mode is missing")
	}
	buttonStart := strings.Index(rendered, `data-device-preview="usb-camera"`)
	if buttonStart < 0 {
		t.Fatal("unconfigured analog camera preview button is missing")
	}
	buttonEnd := strings.Index(rendered[buttonStart:], `</button>`)
	if buttonEnd < 0 || !strings.Contains(rendered[buttonStart:buttonStart+buttonEnd], "disabled") {
		t.Fatal("unconfigured analog camera preview button is not disabled")
	}
}

func TestAnalogCameraPreviewUsesServerMetadataAndCleansStaleSessions(t *testing.T) {
	required := []string{
		`id="device-preview-dialog" class="preview-dialog" aria-labelledby="device-preview-title" aria-describedby="device-preview-metadata"`,
		`id="device-preview-metadata" class="preview-metadata" aria-live="polite"`,
		`id="device-preview-capture"`,
		`id="device-preview-output">Воспроизведение: H.264 / HLS · Битрейт: Динамический (CRF 23)`,
		`id="device-preview-status" class="preview-status" role="status" aria-live="polite"`,
		`previewCapture.textContent = 'Захват: ' + (stream?.pixelFormat || '—')`,
		`(stream?.resolution || '—')`,
		`stream?.fps ? stream.fps + ' FPS'`,
		`stream?.bitrateMode || 'CRF 23'`,
		`renderAnalogPreviewMetadata(result.stream)`,
		`fetch('/api/video-devices/' + encodeURIComponent(deviceID) + '/preview', {method: 'POST'})`,
		`'/preview/' + encodeURIComponent(session.sessionID), {method: 'DELETE', keepalive: true}`,
		`generation !== previewRequestGeneration || !previewDialog.open`,
		`await stopAnalogPreviewSession(startedSession)`,
		`const playerURL = new URL(result.url)`,
		`previewFrame.src = playerURL.toString()`,
		`previewDialog.addEventListener('close'`,
		`previewFrame.src = 'about:blank'`,
		`previewCapture.textContent = ''`,
		`if(dialog.open||previewDialog.open)return`,
	}
	for _, fragment := range required {
		if !strings.Contains(devicesPageHTML, fragment) {
			t.Errorf("analog camera preview UI does not contain %q", fragment)
		}
	}
	if strings.Contains(devicesPageHTML, "previewCapture.innerHTML") {
		t.Fatal("analog preview metadata uses innerHTML for dynamic values")
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
