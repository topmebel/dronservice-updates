package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"DronService/internal/cameranetwork"
	"DronService/internal/cameraproxy"
	"DronService/internal/ipcamera"
)

type cameraNetworkEnsurer interface {
	Ensure(context.Context, string, string, string, string, string, time.Duration) (cameranetwork.Lease, error)
}

func cameraProxyStartHandler(cameras *ipcamera.Service, proxies *cameraproxy.Manager, networks cameraNetworkEnsurer, ttl time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		camera, ok := findCamera(cameras.List(), r.PathValue("cameraID"))
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Камера не найдена"})
			return
		}
		target := cameraproxy.Target{
			ID:            camera.ID,
			Address:       camera.Address,
			ClientAddress: requestClientAddress(r.RemoteAddr),
			HTTPPort:      camera.HTTPPort,
		}
		result, err := proxies.Start(target)
		var lease *cameranetwork.Lease
		var connectivityErr *cameraproxy.ConnectivityError
		if errors.As(err, &connectivityErr) && (connectivityErr.Diagnostic.ConnectError == "no route to host" || connectivityErr.Diagnostic.ConnectError == "network is unreachable" || connectivityErr.Diagnostic.ConnectError == "i/o timeout") {
			created, leaseErr := networks.Ensure(r.Context(), camera.Address, camera.SubnetMask, camera.Gateway, camera.MAC, camera.InterfaceName, ttl)
			if leaseErr != nil {
				message := "Raspberry Pi обнаружила камеру на уровне Ethernet, но не имеет IP-адреса в подсети камеры."
				writeJSON(w, http.StatusBadGateway, map[string]any{"error": message, "state": "helper_failed", "diagnostic": map[string]any{"piIPs": connectivityErr.Diagnostic.PiIPs, "cameraIP": camera.Address, "interface": camera.InterfaceName, "subnetMask": camera.SubnetMask, "connectError": connectivityErr.Diagnostic.ConnectError, "helperError": leaseErr.Error()}})
				return
			}
			lease = &created
			result, err = proxies.Start(target)
		}
		if err != nil {
			if errors.Is(err, cameraproxy.ErrInvalidTarget) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "У камеры некорректный адрес для web-доступа"})
				return
			}
			if errors.As(err, &connectivityErr) {
				message := "Не удалось подключиться к web-интерфейсу камеры"
				if connectivityErr.Diagnostic.ConnectError == "no route to host" || connectivityErr.Diagnostic.ConnectError == "network is unreachable" {
					message = "Raspberry Pi не имеет маршрута до камеры. Reverse proxy не создаёт сетевой маршрут."
				}
				log.Printf("camera proxy connectivity camera=%s route=%s interface=%s error=%s", camera.ID, connectivityErr.Diagnostic.Route, connectivityErr.Diagnostic.Interface, connectivityErr.Diagnostic.ConnectError)
				writeJSON(w, http.StatusBadGateway, map[string]any{"error": message, "diagnostic": connectivityErr.Diagnostic})
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
		response := map[string]any{"mode": "proxy", "state": "proxy_ready", "url": proxyURL, "expiresAt": result.ExpiresAt}
		if lease != nil {
			response["temporaryAddress"] = lease.Address
			response["interface"] = lease.Interface
			response["prefix"] = lease.Prefix
			response["temporaryNetworkExpiresAt"] = lease.ExpiresAt
			response["accessMethod"] = "temporary_network_ready"
		}
		writeJSON(w, http.StatusOK, response)
	}
}

func requestClientAddress(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return strings.Trim(host, "[]")
	}
	return strings.Trim(remoteAddress, "[]")
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
