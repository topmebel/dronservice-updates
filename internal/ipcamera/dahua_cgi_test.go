package ipcamera

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDahuaCGIChangeIPv4UsesDigestAndNetworkParameters(t *testing.T) {
	var configured bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="camera", nonce="nonce", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("action") == "setConfig" {
			configured = true
			for key, want := range map[string]string{"Network.eth0.IPAddress": "192.168.1.40", "Network.eth0.SubnetMask": "255.255.255.0", "Network.eth0.DefaultGateway": "192.168.1.1", "Network.eth0.DhcpEnable": "false"} {
				if got := r.URL.Query().Get(key); got != want {
					t.Errorf("%s = %q, want %q", key, got, want)
				}
			}
		}
		if r.URL.Query().Get("action") == "getConfig" {
			_, _ = w.Write([]byte("table.Network.eth0.IPAddress=192.168.1.108\n"))
			return
		}
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	client := newDahuaCGIClient()
	if err := client.ChangeIPv4(context.Background(), parsed.Hostname(), uint16Port(t, parsed.Port()), "admin", "secret", "192.168.1.40", "255.255.255.0", "192.168.1.1"); err != nil {
		t.Fatal(err)
	}
	if !configured {
		t.Fatal("setConfig was not requested")
	}
}

func TestDahuaCGIChangeIPv4DetectsIndexedNetworkFields(t *testing.T) {
	var configured bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="camera", nonce="nonce", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("action") == "getConfig" {
			_, _ = w.Write([]byte("table.Network.eth0[0].IPAddress=192.168.1.108\n"))
			return
		}
		configured = r.URL.Query().Get("Network.eth0[0].IPAddress") == "192.168.88.40"
		_, _ = w.Write([]byte("OK"))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	if err := newDahuaCGIClient().ChangeIPv4(context.Background(), parsed.Hostname(), uint16Port(t, parsed.Port()), "admin", "secret", "192.168.88.40", "255.255.255.0", "192.168.88.1"); err != nil {
		t.Fatal(err)
	}
	if !configured {
		t.Fatal("indexed Network fields were not used")
	}
}

func TestParseDahuaVideoStreams(t *testing.T) {
	body := strings.Join([]string{
		"table.Encode[0].MainFormat[0].Video.Width=2688",
		"table.Encode[0].MainFormat[0].Video.Height=1520",
		"table.Encode[0].MainFormat[0].Video.FPS=25",
		"table.Encode[0].MainFormat[0].Video.BitRate=4096",
		"Encode[0].ExtraFormat[0].Video.Resolution=704x576",
		"Encode[0].ExtraFormat[0].Video.fps=15",
		"Encode[0].ExtraFormat[0].Video.bitrate=512",
		"table.Encode[1].MainFormat[0].Video.resolution=ignored",
	}, "\r\n")

	mainStream, subStream := parseDahuaVideoStreams(body)
	if mainStream != (VideoStream{Resolution: "2688x1520", FPS: "25", BitrateKbps: 4096}) {
		t.Fatalf("main stream = %#v", mainStream)
	}
	if subStream != (VideoStream{Resolution: "704x576", FPS: "15", BitrateKbps: 512}) {
		t.Fatalf("sub stream = %#v", subStream)
	}
}

func TestDahuaCGIVideoStreamsUsesDigestAndEncodeConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="camera", nonce="nonce", qop="auth"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("action") != "getConfig" || r.URL.Query().Get("name") != "Encode" {
			t.Errorf("query = %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte("table.Encode[0].MainFormat[0].Video.resolution=1920x1080\n" +
			"table.Encode[0].MainFormat[0].Video.FPS=25\n" +
			"table.Encode[0].MainFormat[0].Video.BitRate=2048\n" +
			"table.Encode[0].ExtraFormat[0].Video.resolution=640x480\n" +
			"table.Encode[0].ExtraFormat[0].Video.FPS=12\n" +
			"table.Encode[0].ExtraFormat[0].Video.BitRate=384"))
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)

	mainStream, subStream, err := newDahuaCGIClient().VideoStreams(context.Background(), parsed.Hostname(), uint16Port(t, parsed.Port()), "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if mainStream.Resolution != "1920x1080" || mainStream.FPS != "25" || mainStream.BitrateKbps != 2048 || subStream.Resolution != "640x480" || subStream.FPS != "12" || subStream.BitrateKbps != 384 {
		t.Fatalf("streams = %#v, %#v", mainStream, subStream)
	}
}

func uint16Port(t *testing.T, value string) uint16 {
	t.Helper()
	var port uint16
	if _, err := fmt.Sscanf(value, "%d", &port); err != nil {
		t.Fatal(err)
	}
	return port
}
