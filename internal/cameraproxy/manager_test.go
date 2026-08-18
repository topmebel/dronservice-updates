package cameraproxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"
)

func TestStartReturnsDirectURLForCameraOnLocalNetwork(t *testing.T) {
	manager := NewManager(Config{LocalNetworks: func() ([]*net.IPNet, error) {
		_, network, _ := net.ParseCIDR("192.168.1.0/24")
		return []*net.IPNet{network}, nil
	}})
	result, err := manager.Start(Target{ID: "camera", Address: "192.168.1.20", ClientAddress: "192.168.1.50", HTTPPort: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "direct" || result.DirectURL != "http://192.168.1.20:8080/" {
		t.Fatalf("result = %+v", result)
	}
}

func TestStartProxiesWhenBrowserCannotReachCameraNetwork(t *testing.T) {
	manager := NewManager(Config{
		ListenAddress: "127.0.0.1:0",
		LocalNetworks: func() ([]*net.IPNet, error) {
			_, cameraNetwork, _ := net.ParseCIDR("192.168.50.0/24")
			_, browserNetwork, _ := net.ParseCIDR("192.168.1.0/24")
			return []*net.IPNet{cameraNetwork, browserNetwork}, nil
		},
	})
	t.Cleanup(func() { _ = manager.Close(context.Background()) })

	result, err := manager.Start(Target{ID: "camera", Address: "192.168.50.20", ClientAddress: "192.168.1.50"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Mode != "proxy" || result.Address == "" {
		t.Fatalf("result = %+v, want proxy", result)
	}
}

func TestStartProxiesCameraOutsideLocalNetwork(t *testing.T) {
	camera := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Camera-Path", r.URL.Path)
		_, _ = w.Write([]byte("camera settings"))
	}))
	defer camera.Close()
	cameraURL, _ := url.Parse(camera.URL)
	port, _ := net.LookupPort("tcp", cameraURL.Port())

	manager := NewManager(Config{
		ListenAddress: "127.0.0.1:0",
		TTL:           time.Minute,
		LocalNetworks: func() ([]*net.IPNet, error) { return nil, nil },
	})
	manager.config.allowLoopback = true
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	result, err := manager.Start(Target{ID: "camera", Address: cameraURL.Hostname(), HTTPPort: uint16(port)})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://" + result.Address + "/admin/network")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "camera settings" || response.Header.Get("X-Camera-Path") != "/admin/network" {
		t.Fatalf("response status=%d headers=%v body=%q", response.StatusCode, response.Header, body)
	}
}

func TestStartRejectsUnsafeAddress(t *testing.T) {
	manager := NewManager(Config{})
	for _, address := range []string{"camera.local", "127.0.0.1", "0.0.0.0", "224.0.0.1"} {
		if _, err := manager.Start(Target{ID: "camera", Address: address}); err == nil {
			t.Fatalf("Start(%q) error = nil", address)
		}
	}
}

func TestStartRenewsMatchingProxySession(t *testing.T) {
	camera := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("camera"))
	}))
	defer camera.Close()
	cameraURL, _ := url.Parse(camera.URL)
	port, _ := net.LookupPort("tcp", cameraURL.Port())
	manager := NewManager(Config{
		ListenAddress: "127.0.0.1:0",
		TTL:           time.Minute,
		LocalNetworks: func() ([]*net.IPNet, error) { return nil, nil },
	})
	manager.config.allowLoopback = true
	t.Cleanup(func() { _ = manager.Close(context.Background()) })

	first, err := manager.Start(Target{ID: "camera", Address: cameraURL.Hostname(), HTTPPort: uint16(port)})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	second, err := manager.Start(Target{ID: "camera", Address: cameraURL.Hostname(), HTTPPort: uint16(port)})
	if err != nil {
		t.Fatal(err)
	}
	if second.Address != first.Address || !second.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("first=%+v second=%+v", first, second)
	}

	manager.mu.Lock()
	current := manager.sessions["camera"]
	manager.mu.Unlock()
	manager.stop("camera", current, first.ExpiresAt)
	response, err := http.Get("http://" + second.Address + "/")
	if err != nil {
		t.Fatalf("renewed proxy stopped by stale timer: %v", err)
	}
	_ = response.Body.Close()
}

func TestStartReplacesProxyWhenCameraTargetChanges(t *testing.T) {
	upstream := func(body string) (*httptest.Server, *url.URL, uint16) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		parsed, _ := url.Parse(server.URL)
		port, _ := net.LookupPort("tcp", parsed.Port())
		return server, parsed, uint16(port)
	}
	firstCamera, firstURL, firstPort := upstream("first")
	defer firstCamera.Close()
	secondCamera, secondURL, secondPort := upstream("second")
	defer secondCamera.Close()
	manager := NewManager(Config{
		ListenAddress: "127.0.0.1:0",
		TTL:           time.Minute,
		LocalNetworks: func() ([]*net.IPNet, error) { return nil, nil },
	})
	manager.config.allowLoopback = true
	t.Cleanup(func() { _ = manager.Close(context.Background()) })

	if _, err := manager.Start(Target{ID: "camera", Address: firstURL.Hostname(), HTTPPort: firstPort}); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Start(Target{ID: "camera", Address: secondURL.Hostname(), HTTPPort: secondPort})
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://" + result.Address + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if string(body) != "second" {
		t.Fatalf("replacement proxy body = %q", body)
	}

	manager.mu.Lock()
	handler := manager.sessions["camera"].server.Handler
	manager.mu.Unlock()
	proxy, ok := handler.(*httputil.ReverseProxy)
	var transport *http.Transport
	if ok {
		transport, ok = proxy.Transport.(*http.Transport)
	}
	if !ok || transport.Proxy != nil {
		t.Fatal("camera proxy must connect directly without HTTP_PROXY")
	}
}
