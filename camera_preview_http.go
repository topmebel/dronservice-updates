package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"DronService/internal/ipcamera"
	"DronService/internal/streampreview"
)

type cameraPreviewSource interface {
	PreviewStreamSource(string, string) (ipcamera.StreamSource, error)
}

type cameraPreviewSessions interface {
	Start(context.Context, string, string) (streampreview.Session, error)
	Stop(context.Context, string, string) error
}

type cameraPreviewResponse struct {
	SessionID string                `json:"sessionId"`
	URL       string                `json:"url"`
	ExpiresAt time.Time             `json:"expiresAt"`
	Stream    previewStreamMetadata `json:"stream"`
}

type previewStreamMetadata struct {
	Kind        string `json:"kind"`
	PixelFormat string `json:"pixelFormat,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
	FPS         string `json:"fps,omitempty"`
	BitrateKbps uint32 `json:"bitrateKbps,omitempty"`
	BitrateMode string `json:"bitrateMode,omitempty"`
}

func cameraPreviewStartHandler(cameras cameraPreviewSource, previews cameraPreviewSessions, publicHLSBase string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamKind := r.URL.Query().Get("stream")
		if streamKind == "" {
			streamKind = "main"
		}
		if streamKind != "main" && streamKind != "sub" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Допустимые потоки предпросмотра: main или sub"})
			return
		}

		base, err := parseCameraPreviewHLSBase(publicHLSBase)
		if err != nil {
			log.Printf("prepare camera preview HLS URL: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сформировать адрес предпросмотра"})
			return
		}

		cameraID := r.PathValue("cameraID")
		source, err := cameras.PreviewStreamSource(cameraID, streamKind)
		if err != nil {
			switch {
			case errors.Is(err, ipcamera.ErrCameraNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "Камера не найдена"})
			case errors.Is(err, ipcamera.ErrDahuaInitializationRequired):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "Сначала авторизуйте новую камеру"})
			default:
				writeJSON(w, http.StatusConflict, map[string]string{"error": "Выбранный RTSP-поток камеры не настроен"})
			}
			return
		}

		session, err := previews.Start(r.Context(), cameraID, source.URL)
		if err != nil {
			log.Printf("start camera stream preview camera=%q: %v", cameraID, err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Не удалось запустить предпросмотр камеры"})
			return
		}

		previewURL := cameraPreviewHLSURLFromBase(base, session.Path)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, cameraPreviewResponse{
			SessionID: session.ID,
			URL:       previewURL,
			ExpiresAt: session.ExpiresAt,
			Stream: previewStreamMetadata{
				Kind:        streamKind,
				Resolution:  source.Metadata.Resolution,
				FPS:         source.Metadata.FPS,
				BitrateKbps: source.Metadata.BitrateKbps,
			},
		})
	}
}

func cameraPreviewStopHandler(previews cameraPreviewSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := previews.Stop(r.Context(), r.PathValue("cameraID"), r.PathValue("sessionID"))
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if errors.Is(err, streampreview.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Сессия предпросмотра не найдена"})
			return
		}
		log.Printf("stop camera stream preview: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Не удалось остановить предпросмотр камеры"})
	}
}

func cameraPreviewHLSURL(publicHLSBase, streamPath string) (string, error) {
	base, err := parseCameraPreviewHLSBase(publicHLSBase)
	if err != nil {
		return "", err
	}
	return cameraPreviewHLSURLFromBase(base, streamPath), nil
}

func parseCameraPreviewHLSBase(value string) (*url.URL, error) {
	base, err := url.Parse(strings.TrimSpace(value))
	if err != nil || base.Host == "" || (base.Scheme != "http" && base.Scheme != "https") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("invalid public HLS base URL")
	}
	base.Path = strings.TrimRight(base.Path, "/")
	base.RawPath = ""
	return base, nil
}

func cameraPreviewHLSURLFromBase(base *url.URL, streamPath string) string {
	result := *base
	result.Path += "/" + streamPath
	query := result.Query()
	query.Set("autoplay", "true")
	query.Set("controls", "true")
	query.Set("muted", "true")
	query.Set("playsInline", "true")
	result.RawQuery = query.Encode()
	return result.String()
}
