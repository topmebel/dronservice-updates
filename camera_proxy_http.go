package main

import (
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"DronService/internal/cameraproxy"
	"DronService/internal/ipcamera"
)

func cameraProxyStartHandler(cameras *ipcamera.Service, proxies *cameraproxy.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		camera, ok := findCamera(cameras.List(), r.PathValue("cameraID"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Камера не найдена"})
			return
		}
		result, err := proxies.Start(cameraproxy.Target{ID: camera.ID, Address: camera.Address, HTTPPort: camera.HTTPPort})
		if err != nil {
			if errors.Is(err, cameraproxy.ErrInvalidTarget) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "У камеры некорректный адрес для web-доступа"})
				return
			}
			log.Printf("start camera setup proxy: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось открыть временный доступ к камере"})
			return
		}

		if result.Mode == "direct" {
			writeJSON(w, http.StatusOK, map[string]string{"mode": "direct", "url": result.DirectURL})
			return
		}
		host, err := requestHostname(r.Host)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Некорректный адрес DronService"})
			return
		}
		_, port, err := net.SplitHostPort(result.Address)
		if err != nil {
			log.Printf("camera setup proxy listener address: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Не удалось сформировать адрес временного доступа"})
			return
		}
		proxyURL := (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/"}).String()
		writeJSON(w, http.StatusOK, map[string]any{"mode": "proxy", "url": proxyURL, "expiresAt": result.ExpiresAt})
	}
}

func findCamera(cameras []ipcamera.Camera, id string) (ipcamera.Camera, bool) {
	for _, camera := range cameras {
		if camera.ID == id {
			return camera, true
		}
	}
	return ipcamera.Camera{}, false
}

func requestHostname(hostport string) (string, error) {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	if host == "" || strings.ContainsAny(host, "/\\?#@") {
		return "", errors.New("invalid host")
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 {
			return "", errors.New("invalid host")
		}
		for index, character := range label {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' {
				return "", errors.New("invalid host character at " + strconv.Itoa(index))
			}
		}
	}
	return host, nil
}
