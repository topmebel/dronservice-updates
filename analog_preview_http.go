package main

import (
	"context"
	"errors"
	"log"
	"net/http"

	"DronService/internal/stream"
	"DronService/internal/streampreview"
)

const analogPreviewOwnerPrefix = "analog:"

type analogPreviewSource interface {
	ResolveAnalogPreview(context.Context, string) (stream.Source, error)
}

type analogPreviewSessions interface {
	StartSource(context.Context, string, stream.Source) (streampreview.Session, error)
	Stop(context.Context, string, string) error
}

func analogPreviewStartHandler(sources analogPreviewSource, previews analogPreviewSessions, publicHLSBase string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base, err := parseCameraPreviewHLSBase(publicHLSBase)
		if err != nil {
			log.Printf("prepare analog preview HLS URL: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сформировать адрес предпросмотра"})
			return
		}

		deviceID := r.PathValue("deviceID")
		source, err := sources.ResolveAnalogPreview(r.Context(), deviceID)
		if err != nil {
			switch {
			case errors.Is(err, errAnalogPreviewUnavailable):
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "FFmpeg для предпросмотра аналоговой камеры не установлен"})
			case errors.Is(err, errAnalogDeviceNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "Аналоговая камера не подключена"})
			case errors.Is(err, errAnalogDeviceNotConfigured), errors.Is(err, errAnalogDeviceModeInvalid):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "Сохранённый режим аналоговой камеры недоступен"})
			default:
				log.Printf("resolve analog camera preview device=%q: %v", deviceID, err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось проверить аналоговую камеру"})
			}
			return
		}

		ownerID := analogPreviewOwnerPrefix + deviceID
		session, err := previews.StartSource(r.Context(), ownerID, source)
		if err != nil {
			log.Printf("start analog camera preview device=%q: %v", deviceID, err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Не удалось запустить предпросмотр аналоговой камеры"})
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, cameraPreviewResponse{
			SessionID: session.ID,
			URL:       cameraPreviewHLSURLFromBase(base, session.Path),
			ExpiresAt: session.ExpiresAt,
			Stream: previewStreamMetadata{
				Kind:        "analog",
				PixelFormat: source.PixelFormat,
				Resolution:  source.Resolution,
				FPS:         source.FPS,
				BitrateMode: "CRF 23",
			},
		})
	}
}

func analogPreviewStopHandler(previews analogPreviewSessions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deviceID := r.PathValue("deviceID")
		err := previews.Stop(r.Context(), analogPreviewOwnerPrefix+deviceID, r.PathValue("sessionID"))
		if err == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if errors.Is(err, streampreview.ErrSessionNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Сессия предпросмотра не найдена"})
			return
		}
		log.Printf("stop analog camera preview device=%q: %v", deviceID, err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "Не удалось остановить предпросмотр аналоговой камеры"})
	}
}
