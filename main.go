package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"DronService/internal/buildinfo"
	"DronService/internal/cameranetwork"
	"DronService/internal/cameraproxy"
	"DronService/internal/deviceconfig"
	"DronService/internal/ipcamera"
	"DronService/internal/mavlink"
	"DronService/internal/mediamtx"
	"DronService/internal/starlink"
	"DronService/internal/stream"
	"DronService/internal/streampreview"
	"DronService/internal/updater"
	"DronService/internal/v4l2"
	"DronService/internal/zerotier"
)

type HealthResponse struct {
	Status string `json:"status"`
}

const defaultMonitorInterval = 5 * time.Second

func monitorInterval(value string) time.Duration {
	interval, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil || interval <= 0 {
		return defaultMonitorInterval
	}
	return interval
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(buildinfo.Version)
		return
	}
	mediaMTXURL := os.Getenv("MEDIAMTX_URL")
	if mediaMTXURL == "" {
		mediaMTXURL = "http://127.0.0.1:9997"
	}

	mediaMTXClient := mediamtx.NewClient(
		mediaMTXURL,
		os.Getenv("MEDIAMTX_USER"),
		os.Getenv("MEDIAMTX_PASSWORD"),
	)
	mediaMTXConfigPath := strings.TrimSpace(os.Getenv("MEDIAMTX_CONFIG_PATH"))
	if mediaMTXConfigPath == "" {
		mediaMTXConfigPath = defaultMediaMTXConfigPath()
	}
	mediaMTXConfigFile := mediamtx.NewConfigFile(mediaMTXConfigPath)
	streamService := stream.NewService(mediaMTXClient, mediaMTXConfigFile)
	deviceScanner := v4l2.NewScanner()
	dataDir := os.Getenv("DRONSERVICE_DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/lib/dronservice"
	}
	starlinkService, err := starlink.NewService(dataDir)
	if err != nil {
		log.Fatalf("prepare Starlink monitor: %v", err)
	}
	applicationUpdater, err := updater.NewClient(updater.Config{
		Repository:     os.Getenv("DRONSERVICE_UPDATE_REPOSITORY"),
		CurrentVersion: buildinfo.Version,
		RequestPath:    filepath.Join(dataDir, "update-dronservice.request"),
		StatusPath:     filepath.Join(dataDir, "update-dronservice.status.json"),
	})
	if err != nil {
		log.Fatalf("prepare application updater: %v", err)
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "arm64" && strings.TrimSpace(os.Getenv("DRONSERVICE_UPDATE_REPOSITORY")) == "" {
		log.Printf("application updates disabled: configure DRONSERVICE_UPDATE_REPOSITORY in /etc/dronservice/update.conf")
	}
	deviceStore, err := deviceconfig.NewStore(dataDir)
	if err != nil {
		log.Fatalf("prepare device configuration: %v", err)
	}
	mavlinkStore, err := mavlink.NewStore(dataDir)
	if err != nil {
		log.Fatalf("prepare MAVLink configuration: %v", err)
	}
	mavlinkService, err := mavlink.NewService(mavlinkStore)
	if err != nil {
		log.Fatalf("prepare MAVLink service: %v", err)
	}
	devicesPage, err := newDevicesPageHandler(deviceScanner, deviceStore)
	if err != nil {
		log.Fatalf("prepare devices page: %v", err)
	}
	mediaMTXInstaller := mediamtx.NewInstaller("/usr/local/bin/mediamtx", filepath.Join(dataDir, "install-mediamtx.request"))
	ipCameraService, err := ipcamera.NewService(dataDir, ipcamera.DahuaDiscoverOptions{
		InterfaceName: os.Getenv("DRONSERVICE_DISCOVERY_INTERFACE"),
		Timeout:       5 * time.Second,
		IncludeLegacy: strings.EqualFold(os.Getenv("DRONSERVICE_DISCOVERY_LEGACY"), "true"),
	})
	if err != nil {
		log.Fatalf("prepare IP camera service: %v", err)
	}
	cameraProxyTTL := 15 * time.Minute
	if configuredTTL, parseErr := time.ParseDuration(os.Getenv("DRONSERVICE_CAMERA_PROXY_TTL")); parseErr == nil && configuredTTL > 0 {
		cameraProxyTTL = configuredTTL
	}
	cameraProxyManager := cameraproxy.NewManager(cameraproxy.Config{
		ListenAddress: os.Getenv("DRONSERVICE_CAMERA_PROXY_ADDR"),
		TTL:           cameraProxyTTL,
	})
	cameraNetworkClient := cameranetwork.NewClient(dataDir, 10*time.Second)
	streamSources := streamSourceCatalog{scanner: deviceScanner, devices: deviceStore, ipCameras: ipCameraService, ffmpegPath: "/usr/bin/ffmpeg"}
	publicRTSPBase := os.Getenv("DRONSERVICE_RTSP_PUBLIC_URL")
	if publicRTSPBase == "" {
		publicRTSPBase = "rtsp://dronservice.local:554"
	}
	publicHLSBase := strings.TrimSpace(os.Getenv("DRONSERVICE_HLS_PUBLIC_URL"))
	if publicHLSBase == "" {
		publicHLSBase = "http://" + net.JoinHostPort(preferredRTSPHost(), "8888")
	}
	if _, err := parseCameraPreviewHLSBase(publicHLSBase); err != nil {
		log.Fatalf("validate public HLS URL: %v", err)
	}
	streamPreviewTTL := 10 * time.Minute
	if configuredTTL, parseErr := time.ParseDuration(os.Getenv("DRONSERVICE_STREAM_PREVIEW_TTL")); parseErr == nil && configuredTTL > 0 {
		streamPreviewTTL = configuredTTL
	}
	streamPreviewManager := streampreview.NewManager(streamService, streamPreviewTTL)
	previewCleanupCtx, previewCleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := streamPreviewManager.Cleanup(previewCleanupCtx); err != nil {
		log.Printf("clean stale stream previews: %v", err)
	}
	previewCleanupCancel()
	ipCamerasPage, err := newIPCamerasPageHandler(ipCameraService)
	if err != nil {
		log.Fatalf("prepare IP cameras page: %v", err)
	}
	zeroTierTokenFile := os.Getenv("ZEROTIER_TOKEN_FILE")
	if zeroTierTokenFile == "" {
		if credentialsDirectory := os.Getenv("CREDENTIALS_DIRECTORY"); credentialsDirectory != "" {
			zeroTierTokenFile = filepath.Join(credentialsDirectory, "zerotier-token")
		} else {
			zeroTierTokenFile = "/var/lib/zerotier-one/authtoken.secret"
		}
	}
	zeroTierClient, err := zerotier.NewClient(os.Getenv("ZEROTIER_URL"), zeroTierTokenFile)
	if err != nil {
		log.Fatalf("prepare ZeroTier client: %v", err)
	}
	zeroTierUpdater := zerotier.NewUpdater(filepath.Join(dataDir, "update-zerotier.request"))
	streamsPage, err := newStreamsPageHandler(streamService, streamSources, mediaMTXInstaller, zeroTierClient, publicRTSPBase, publicHLSBase)
	if err != nil {
		log.Fatalf("prepare streams page: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/devices", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /api/health", healthHandler)
	mux.HandleFunc("GET /api/version", versionHandler)
	mux.HandleFunc("GET /api/update/status", applicationUpdateStatusHandler(applicationUpdater))
	mux.HandleFunc("POST /api/update", applicationUpdateRequestHandler(applicationUpdater))
	mux.HandleFunc("GET /assets/application-status.js", applicationStatusScriptHandler)
	mux.HandleFunc("GET /assets/application.css", applicationStyleHandler)
	mux.HandleFunc("GET /api/system/internet", internetStatusHandler(starlinkService))
	mux.HandleFunc("POST /api/system/starlink/reboot", starlinkRebootHandler(starlinkService))
	mux.HandleFunc("GET /api/starlink", starlinkStatusHandler(starlinkService))
	mux.HandleFunc("GET /api/system/network", networkStatusHandler)
	mux.HandleFunc("/api/telemetry/config", flightControllerConfigHandler(mavlinkService))
	mux.HandleFunc("GET /api/telemetry/status", flightControllerStatusHandler(mavlinkService))
	mux.HandleFunc("/api/flight-controller/config", flightControllerConfigHandler(mavlinkService))
	mux.HandleFunc("GET /api/flight-controller/status", flightControllerStatusHandler(mavlinkService))
	mux.HandleFunc("GET /api/streams", streamsHandler(streamService))
	mux.HandleFunc("/api/stream-configs", streamConfigsHandler(streamService, streamSources, publicRTSPBase))
	mux.HandleFunc("/api/mediamtx/install", mediaMTXInstallHandler(mediaMTXInstaller))
	mux.HandleFunc("/api/mediamtx/config-file", mediaMTXConfigFileHandler(mediaMTXConfigFile))
	mux.HandleFunc("/api/ip-cameras", ipCamerasHandler(ipCameraService))
	mux.HandleFunc("DELETE /api/ip-cameras/{cameraID}", deleteIPCameraHandler(ipCameraService))
	mux.HandleFunc("POST /api/ip-cameras/discover", ipCameraDiscoveryHandler(ipCameraService))
	mux.HandleFunc("POST /api/ip-cameras/status", ipCameraStatusHandler(ipCameraService))
	mux.HandleFunc("POST /api/ip-cameras/{cameraID}/video-streams", ipCameraVideoStreamsHandler(ipCameraService))
	mux.HandleFunc("POST /api/ip-cameras/{cameraID}/preview", cameraPreviewStartHandler(ipCameraService, streamPreviewManager, publicHLSBase))
	mux.HandleFunc("DELETE /api/ip-cameras/{cameraID}/preview/{sessionID}", cameraPreviewStopHandler(streamPreviewManager))
	mux.HandleFunc("POST /api/ip-cameras/{cameraID}/setup-access", cameraProxyStartHandler(ipCameraService, cameraProxyManager, cameraNetworkClient, cameraProxyTTL))
	mux.HandleFunc("GET /api/zerotier", zeroTierStatusHandler(zeroTierClient, zeroTierUpdater))
	mux.HandleFunc("POST /api/zerotier/update", zeroTierUpdateHandler(zeroTierUpdater))
	mux.HandleFunc("POST /api/zerotier/networks", zeroTierJoinHandler(zeroTierClient))
	mux.HandleFunc("DELETE /api/zerotier/networks/{networkID}", zeroTierLeaveHandler(zeroTierClient))
	mux.HandleFunc("GET /api/video-devices", videoDevicesHandler(deviceScanner, deviceStore))
	mux.HandleFunc("POST /api/video-devices/config", saveVideoDeviceHandler(deviceScanner, deviceStore))
	mux.HandleFunc("DELETE /api/video-devices/config/{deviceID}", deleteVideoDeviceConfigHandler(deviceStore))
	mux.HandleFunc("POST /api/video-devices/{deviceID}/preview", analogPreviewStartHandler(streamSources, streamPreviewManager, publicHLSBase))
	mux.HandleFunc("DELETE /api/video-devices/{deviceID}/preview/{sessionID}", analogPreviewStopHandler(streamPreviewManager))
	mux.Handle("GET /devices", devicesPage)
	mux.Handle("GET /streams", streamsPage)
	mux.Handle("GET /ip-cameras", ipCamerasPage)
	mux.HandleFunc("GET /starlink", starlinkPageHandler)
	mux.HandleFunc("GET /telemetry", flightControllerPageHandler)
	mux.HandleFunc("GET /flight-controller", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/telemetry", http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /zerotier", zeroTierPageHandler)

	listenAddress := os.Getenv("DRONSERVICE_ADDR")
	if listenAddress == "" {
		listenAddress = ":80"
	}

	server := &http.Server{
		Addr:              listenAddress,
		Handler:           secureHTTPHandler(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go ipCameraService.Monitor(shutdownSignal, monitorInterval(os.Getenv("DRONSERVICE_CAMERA_MONITOR_INTERVAL")))
	go mavlinkService.Run(shutdownSignal)

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-shutdownSignal.Done()

		httpShutdownCtx, cancelHTTPShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		if err := server.Shutdown(httpShutdownCtx); err != nil {
			log.Printf("HTTP server shutdown: %v", err)
		}
		cancelHTTPShutdown()

		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelCleanup()
		if err := cameraProxyManager.Close(cleanupCtx); err != nil {
			log.Printf("camera setup proxy shutdown: %v", err)
		}
		if err := streamPreviewManager.Close(cleanupCtx); err != nil {
			log.Printf("camera stream preview shutdown: %v", err)
		}
	}()

	log.Printf("DronService started on %s", server.Addr)
	log.Printf("MediaMTX URL: %s", mediaMTXURL)

	serveErr := server.ListenAndServe()
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		log.Fatalf("HTTP server: %v", serveErr)
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		<-shutdownDone
	}
}

func defaultMediaMTXConfigPath() string {
	const currentPath = "/usr/local/etc/mediamtx/mediamtx.yml"
	const legacyPath = "/usr/local/etc/mediamtx.yml"

	if _, err := os.Stat(currentPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return currentPath
	}
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath
	}
	return currentPath
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, HealthResponse{Status: "ok"})
}

func streamsHandler(service *stream.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streams, err := service.List(r.Context())
		if err != nil {
			log.Printf("list streams: %v", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "stream service unavailable"})
			return
		}

		writeJSON(w, http.StatusOK, stream.ListResponse{Streams: streams})
	}
}

func videoDevicesHandler(scanner *v4l2.Scanner, store *deviceconfig.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices, err := configuredDevices(r.Context(), scanner, store)
		if err != nil {
			log.Printf("scan video devices: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "video device scan failed"})
			return
		}

		writeJSON(w, http.StatusOK, v4l2.ListResponse{Devices: devices})
	}
}

func newDevicesPageHandler(scanner *v4l2.Scanner, store *deviceconfig.Store) (http.Handler, error) {
	page, err := template.New("devices").Parse(devicesPageHTML)
	if err != nil {
		return nil, fmt.Errorf("parse devices page template: %w", err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		devices, err := configuredDevices(r.Context(), scanner, store)
		if err != nil {
			log.Printf("scan video devices: %v", err)
			http.Error(w, "video device scan failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := page.Execute(w, struct{ Devices []v4l2.Device }{Devices: devices}); err != nil {
			log.Printf("render devices page: %v", err)
		}
	}), nil
}

func configuredDevices(ctx context.Context, scanner *v4l2.Scanner, store *deviceconfig.Store) ([]v4l2.Device, error) {
	devices, err := scanner.Scan(ctx)
	if err != nil {
		return nil, err
	}
	for index := range devices {
		config, ok := store.Get(devices[index].ID)
		if !ok {
			continue
		}
		devices[index].ConfiguredName = config.Name
		devices[index].SelectedFormat = config.PixelFormat
		devices[index].SelectedResolution = config.Resolution
		devices[index].SelectedFPS = config.FPS
		devices[index].Use = config.Use
	}
	return devices, nil
}

func saveVideoDeviceHandler(scanner *v4l2.Scanner, store *deviceconfig.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var config deviceconfig.Config
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid configuration"})
			return
		}
		config.Name = strings.TrimSpace(config.Name)
		config.DevicePath = strings.TrimSpace(config.DevicePath)
		if config.Name == "" || len(config.Name) > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "camera name is required and must not exceed 100 characters"})
			return
		}

		devices, err := scanner.Scan(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "video device scan failed"})
			return
		}
		if config.DevicePath == "" {
			for _, device := range devices {
				if device.ID == config.DeviceID {
					config.DevicePath = device.Path
					break
				}
			}
		}
		if !validDeviceConfig(devices, config) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device or stream mode is not available"})
			return
		}
		if err := store.Save(config); err != nil {
			log.Printf("save video device configuration: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "configuration could not be saved"})
			return
		}
		writeJSON(w, http.StatusOK, config)
	}
}

func deleteVideoDeviceConfigHandler(store *deviceconfig.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := store.Delete(r.PathValue("deviceID")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "Настройки камеры не найдены"})
				return
			}
			log.Printf("delete video device configuration: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось удалить настройки камеры"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func validDeviceConfig(devices []v4l2.Device, config deviceconfig.Config) bool {
	for _, device := range devices {
		if device.ID != config.DeviceID || device.Path != config.DevicePath {
			continue
		}
		for _, format := range device.Formats {
			if format.PixelFormat != config.PixelFormat {
				continue
			}
			for _, mode := range format.Modes {
				if mode.Resolution == config.Resolution && mode.FPS == config.FPS {
					return true
				}
			}
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write JSON response: %v", err)
	}
}

const devicesPageHTML = `<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <script src="/assets/application-status.js" defer></script>
  <link rel="stylesheet" href="/assets/application.css">
  <title>DronService — список аналоговых камер</title>
</head>
<body>
<div class="app-shell">
<aside class="app-sidebar">
  <nav class="main-nav"><span class="brand">DronService · <small id="app-version">…</small></span><span id="internet-status">Интернет: проверка…</span><a class="active" href="/devices">Аналог. камеры</a><a href="/ip-cameras">IP-камеры</a><a href="/streams">MediaMTX</a><a href="/starlink">Starlink</a><a href="/telemetry">Телеметрия</a><a href="/zerotier">ZeroTier</a></nav>
  <div id="network-info"><div id="network-addresses">Сеть: получение адресов…</div><button id="update-app" type="button" hidden style="padding:5px 9px">Обновить</button><span id="update-app-state"></span></div>
</aside>
<main class="app-main">
  <div class="page-heading"><h1>Список аналоговых камер</h1></div>
  {{if .Devices}}
  <div class="camera-grid" id="devices-body">
    {{range .Devices}}
    <article class="camera-card" data-device-id="{{.ID}}" data-device-path="{{.Path}}" title="Двойной клик для выбора режима">
      <div class="camera-card-header">
        <div class="camera-card-title">
          {{if .ConfiguredName}}<span class="camera-name">{{.ConfiguredName}}</span>{{else}}<span class="camera-name muted">Не настроена</span>{{end}}
          <code class="camera-path">{{.Path}}</code>{{if .Error}}<span class="error">{{.Error}}</span>{{end}}
        </div>
        <div class="camera-card-controls">
          <span class="mediamtx-badge {{if .Use}}enabled{{else}}disabled{{end}}" title="{{if .Use}}Используется в MediaMTX{{else}}Не используется в MediaMTX{{end}}">MediaMTX</span>
        </div>
      </div>
      <div class="stream-details-panel"><span class="stream-chip"><strong>Захват</strong><span data-device-stream>{{if and .SelectedFormat .SelectedResolution .SelectedFPS}}{{.SelectedFormat}} · {{.SelectedResolution}} · {{.SelectedFPS}} FPS{{else}}Режим не настроен{{end}}</span><button type="button" class="btn-secondary-sm camera-link preview-button" data-device-preview="{{.ID}}" aria-label="Открыть предпросмотр аналоговой камеры {{if .ConfiguredName}}{{.ConfiguredName}}{{else}}{{.Path}}{{end}}" {{if and .SelectedFormat .SelectedResolution .SelectedFPS}}{{else}}disabled title="Сначала выберите формат, разрешение и FPS"{{end}}>Просмотр ▶</button></span></div>
      <div class="camera-card-actions">{{if .ConfiguredName}}<button type="button" class="btn-danger-sm danger" data-delete-device="{{.ID}}" aria-label="Удалить настройки камеры {{.ConfiguredName}}">Удалить</button>{{else}}<span class="camera-meta">Настройки не сохранены</span>{{end}}</div>
    </article>
    {{end}}
  </div>
  {{else}}<p class="empty">V4L2-устройства не обнаружены.</p>{{end}}
  <dialog id="modes-dialog">
    <h2 id="dialog-title">Режимы камеры</h2>
    <div class="camera-name-row"><div class="camera-name-field"><label for="camera-name">Имя камеры</label><input id="camera-name" maxlength="100" required></div><label class="media-toggle-label"><span>Использовать в MediaMTX</span><input id="use-camera" type="checkbox"></label></div>
    <label for="format-select">Формат стрима</label>
    <select id="format-select"></select>
    <label for="mode-select">Разрешение и FPS</label>
    <select id="mode-select"></select>
    <div class="actions"><button id="close-dialog" type="button">Закрыть</button> <button id="save-camera" type="button">Сохранить</button></div>
  </dialog>
  <dialog id="device-preview-dialog" class="preview-dialog" aria-labelledby="device-preview-title" aria-describedby="device-preview-metadata">
    <div class="preview-header"><h2 id="device-preview-title">Предпросмотр аналоговой камеры</h2><button id="device-preview-close" type="button">Закрыть</button></div>
    <div id="device-preview-metadata" class="preview-metadata" aria-live="polite"><p id="device-preview-capture"></p><p id="device-preview-output">Воспроизведение: H.264 / HLS · Битрейт: Динамический (CRF 23)</p></div>
    <iframe id="device-preview-frame" class="preview-frame" title="Предпросмотр аналоговой камеры" scrolling="no" sandbox="allow-scripts allow-same-origin" allow="autoplay; fullscreen" referrerpolicy="no-referrer"></iframe>
    <p id="device-preview-status" class="preview-status" role="status" aria-live="polite"></p>
  </dialog>
  <script>
    const dialog = document.querySelector('#modes-dialog');
    const title = document.querySelector('#dialog-title');
    const cameraName = document.querySelector('#camera-name');
    const useCamera = document.querySelector('#use-camera');
    const formatSelect = document.querySelector('#format-select');
    const modeSelect = document.querySelector('#mode-select');
    const previewDialog = document.querySelector('#device-preview-dialog');
    const previewTitle = document.querySelector('#device-preview-title');
    const previewCapture = document.querySelector('#device-preview-capture');
    const previewOutput = document.querySelector('#device-preview-output');
    const previewFrame = document.querySelector('#device-preview-frame');
    const previewStatus = document.querySelector('#device-preview-status');
    let selectedDevice;
    let previewSession = null;
    let previewRequestGeneration = 0;
    let previewTrigger = null;

    function renderAnalogPreviewMetadata(stream) {
      previewCapture.textContent = 'Захват: ' + (stream?.pixelFormat || '—') + ' · ' + (stream?.resolution || '—') + ' · ' + (stream?.fps ? stream.fps + ' FPS' : '—');
      previewOutput.textContent = 'Воспроизведение: H.264 / HLS · Битрейт: Динамический (' + (stream?.bitrateMode || 'CRF 23') + ')';
    }

    async function stopAnalogPreviewSession(session) {
      if (!session) return;
      try {
        await fetch('/api/video-devices/' + encodeURIComponent(session.deviceID) + '/preview/' + encodeURIComponent(session.sessionID), {method: 'DELETE', keepalive: true});
      } catch (error) {}
    }

    async function clearAnalogPreview() {
      const session = previewSession;
      previewSession = null;
      previewRequestGeneration++;
      previewFrame.src = 'about:blank';
      previewCapture.textContent = '';
      previewStatus.textContent = '';
      previewDialog.removeAttribute('aria-busy');
      await stopAnalogPreviewSession(session);
    }

    async function openAnalogPreview(button) {
      const deviceID = button.dataset.devicePreview;
      const card = button.closest('.camera-card');
      const name = card?.querySelector('.camera-name:not(.muted)')?.textContent?.trim() || card?.querySelector('.camera-path')?.textContent?.trim() || 'Аналоговая камера';
      const generation = ++previewRequestGeneration;
      let startedSession = null;
      previewTrigger = button;
      button.disabled = true;
      previewTitle.textContent = 'Предпросмотр · ' + name;
      previewFrame.title = 'Предпросмотр аналоговой камеры ' + name;
      previewFrame.src = 'about:blank';
      previewCapture.textContent = 'Захват: проверка сохранённых настроек…';
      previewStatus.textContent = 'Подготовка HLS-потока…';
      previewDialog.setAttribute('aria-busy', 'true');
      previewDialog.showModal();
      try {
        const response = await fetch('/api/video-devices/' + encodeURIComponent(deviceID) + '/preview', {method: 'POST'});
        const result = await response.json().catch(() => ({}));
        if (!response.ok) throw new Error(result.error || 'Не удалось запустить предпросмотр аналоговой камеры');
        if (!result.sessionId || !result.url || !result.stream) throw new Error('Сервис вернул неполные данные предпросмотра');
        startedSession = {deviceID: deviceID, sessionID: result.sessionId};
        if (generation !== previewRequestGeneration || !previewDialog.open) {
          await stopAnalogPreviewSession(startedSession);
          startedSession = null;
          return;
        }
        const playerURL = new URL(result.url);
        if (!/^https?:$/.test(playerURL.protocol) || playerURL.username || playerURL.password) throw new Error('Получен недопустимый адрес предпросмотра');
        renderAnalogPreviewMetadata(result.stream);
        previewSession = startedSession;
        startedSession = null;
        playerURL.searchParams.set('controls', 'true');
        playerURL.searchParams.set('muted', 'true');
        playerURL.searchParams.set('autoplay', 'true');
        playerURL.searchParams.set('playsInline', 'true');
        previewFrame.src = playerURL.toString();
        previewStatus.textContent = '';
      } catch (error) {
        await stopAnalogPreviewSession(startedSession);
        if (generation === previewRequestGeneration && previewDialog.open) previewStatus.textContent = error.message;
      } finally {
        if (button.isConnected) button.disabled = false;
        if (generation === previewRequestGeneration) previewDialog.removeAttribute('aria-busy');
      }
    }

    function fillModes() {
      modeSelect.replaceChildren();
      const format = selectedDevice?.formats[formatSelect.selectedIndex];
      if (!format || format.modes.length === 0) {
        modeSelect.add(new Option('Комбинации не обнаружены', ''));
        modeSelect.disabled = true;
        return;
      }
      modeSelect.disabled = false;
      for (const mode of format.modes) {
        modeSelect.add(new Option(mode.resolution + ' — ' + mode.fps + ' FPS', mode.resolution + '@' + mode.fps));
      }
    }

    async function openModes(path) {
      const response = await fetch('/api/video-devices');
      if (!response.ok) throw new Error('Не удалось получить режимы камеры');
      const data = await response.json();
      selectedDevice = data.devices.find(device => device.path === path);
      if (!selectedDevice) throw new Error('Камера больше не подключена');

      title.textContent = (selectedDevice.card || selectedDevice.name) + ' (' + selectedDevice.path + ')';
      cameraName.value = selectedDevice.configuredName || '';
      useCamera.checked = !!selectedDevice.use;
      formatSelect.replaceChildren();
      for (const format of selectedDevice.formats) {
        const label = format.description ? format.pixelFormat + ' — ' + format.description : format.pixelFormat;
        formatSelect.add(new Option(label, format.pixelFormat));
      }
      if (selectedDevice.formats.length === 0) {
        formatSelect.add(new Option('Форматы не обнаружены', ''));
        formatSelect.disabled = true;
      } else {
        formatSelect.disabled = false;
        const savedFormat = selectedDevice.formats.findIndex(format => format.pixelFormat === selectedDevice.selectedFormat);
        if (savedFormat >= 0) formatSelect.selectedIndex = savedFormat;
      }
      fillModes();
      const selectedFormat = selectedDevice.formats[formatSelect.selectedIndex];
      if (selectedFormat) {
        const savedMode = selectedFormat.modes.findIndex(mode => mode.resolution === selectedDevice.selectedResolution && mode.fps === selectedDevice.selectedFps);
        if (savedMode >= 0) modeSelect.selectedIndex = savedMode;
      }
      dialog.showModal();
    }

    const devicesBody=document.querySelector('#devices-body');devicesBody?.addEventListener('dblclick',event=>{if(event.target.closest('button'))return;const card=event.target.closest('.camera-card[data-device-path]');if(card)openModes(card.dataset.devicePath).catch(error=>window.alert(error.message))});
    devicesBody?.addEventListener('click', async event => {
      const deleteButton = event.target.closest('[data-delete-device]');
      if (deleteButton) {
        if (!confirm('Удалить сохранённые настройки этой камеры?')) return;
        deleteButton.disabled = true;
        const response = await fetch('/api/video-devices/config/' + encodeURIComponent(deleteButton.dataset.deleteDevice), {method: 'DELETE'});
        if (!response.ok) {
          const result = await response.json().catch(() => ({}));
          window.alert(result.error || 'Не удалось удалить настройки камеры');
          deleteButton.disabled = false;
          return;
        }
        window.location.reload();
        return;
      }
	  const button = event.target.closest('[data-device-preview]');
      if (button && !button.disabled) void openAnalogPreview(button);
    });
    formatSelect.addEventListener('change', fillModes);
    document.querySelector('#close-dialog').addEventListener('click', () => dialog.close());
    document.querySelector('#device-preview-close').addEventListener('click', () => previewDialog.close());
    previewDialog.addEventListener('close', () => {
      const trigger = previewTrigger;
      previewTrigger = null;
      void clearAnalogPreview();
      if (trigger?.isConnected) trigger.focus();
    });
    document.querySelector('#save-camera').addEventListener('click', async () => {
      const format = selectedDevice?.formats[formatSelect.selectedIndex];
      const mode = format?.modes[modeSelect.selectedIndex];
      if (!cameraName.value.trim() || !format || !mode) {
        window.alert('Укажите имя камеры и выберите режим стрима');
        return;
      }
      const response = await fetch('/api/video-devices/config', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({
          deviceId: selectedDevice.id,
          devicePath: selectedDevice.path,
          name: cameraName.value.trim(),
          pixelFormat: format.pixelFormat,
          resolution: mode.resolution,
          fps: mode.fps,
          use: useCamera.checked
        })
      });
      if (!response.ok) {
        const result = await response.json().catch(() => ({}));
        window.alert(result.error || 'Не удалось сохранить настройки');
        return;
      }
      dialog.close();
      window.location.reload();
    });
    dialog.addEventListener('click', event => {
      if (event.target === dialog) dialog.close();
    });
    async function refreshDevices(){if(dialog.open||previewDialog.open)return;try{const html=await fetch('/devices',{cache:'no-store'}).then(r=>r.text());const next=new DOMParser().parseFromString(html,'text/html').querySelector('#devices-body');const current=document.querySelector('#devices-body');if(next&&current)current.replaceChildren(...next.children)}catch(error){}}
    const refreshTimer=window.setInterval(refreshDevices,5000);
    function updateInternetStatus(){fetch('/api/system/internet').then(r=>{if(!r.ok)throw new Error('status unavailable');return r.json()}).then(s=>{const e=document.querySelector('#internet-status'),states={online:['есть','#86efac'],offline:['нет','#fca5a5'],unknown:['не удалось проверить','#fbbf24']},state=states[s.status]||states[s.online?'online':'unknown'],sl=s.starlink;if(sl?.internetViaStarlink){e.textContent='Starlink: ↓'+Number(sl.downlinkMbps||0).toFixed(2)+' ↑'+Number(sl.uplinkMbps||0).toFixed(2)+' Мбит/с · '+Number(sl.pingMs||s.pingMs||0).toFixed(1)+' мс';e.title='Подключение '+(sl.topology==='direct'?'напрямую к Starlink':'через роутер')+' · текущая скорость обмена'}else{e.textContent='Интернет: '+state[0]+(sl?.detected?' · Starlink найден':'')}e.style.color=state[1]}).catch(()=>{const e=document.querySelector('#internet-status');e.textContent='Интернет: статус недоступен';e.style.color='#9ca3af'}).finally(()=>setTimeout(updateInternetStatus,10000))}updateInternetStatus();
    document.querySelectorAll('.main-nav a').forEach(link => {
      link.addEventListener('click', () => window.clearInterval(refreshTimer));
    });
  </script>
</main>
</div>
</body>
</html>`
