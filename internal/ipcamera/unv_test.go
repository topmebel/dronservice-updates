package ipcamera

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestNormalizeMAC(t *testing.T) {
	for _, input := range []string{"88:26:3f:7e:7d:da", "88-26-3F-7E-7D-DA", "88263f7e7dda"} {
		if got := normalizeMAC(input); got != "88:26:3f:7e:7d:da" {
			t.Fatalf("normalizeMAC(%q) = %q", input, got)
		}
	}
}

func TestParseUNVAnnouncementOtherSubnetAndOUI(t *testing.T) {
	payload := append([]byte("model=IPC serial=123 "), []byte{0x88, 0x26, 0x3f, 0x7e, 0x7d, 0xda}...)
	device, err := ParseUNVAnnouncement(payload, &net.UDPAddr{IP: net.ParseIP("192.168.4.107"), Port: 43165}, "eth0")
	if err != nil {
		t.Fatal(err)
	}
	if device.IP.String() != "192.168.4.107" || device.MAC != "88:26:3f:7e:7d:da" || device.Confidence != "high" {
		t.Fatalf("unexpected device: %+v", device)
	}
}

func TestUnknownUDPIsNeverHighConfidence(t *testing.T) {
	payload := make([]byte, 2400)
	copy(payload, "proprietary camera announcement printable data")
	device, err := ParseUNVAnnouncement(payload, &net.UDPAddr{IP: net.ParseIP("10.0.0.2")}, "eth0")
	if err != nil {
		t.Fatal(err)
	}
	if device.Confidence == "high" {
		t.Fatal("unknown UDP payload received high confidence")
	}
}

func TestMalformedUNVPayload(t *testing.T) {
	for _, payload := range [][]byte{nil, {1}, []byte("random")} {
		if _, err := ParseUNVAnnouncement(payload, nil, ""); err == nil {
			t.Fatalf("expected error for %x", payload)
		}
	}
}

func TestARPParsing(t *testing.T) {
	frame := arpFrame(t, "88:26:3f:7e:7d:da", "192.168.4.107", "192.168.4.1")
	got, err := parseARPFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if got.SenderIP.String() != "192.168.4.107" || got.TargetIP.String() != "192.168.4.1" || got.Gratuitous {
		t.Fatalf("unexpected ARP: %+v", got)
	}
	gratuitous, err := parseARPFrame(arpFrame(t, "88:26:3f:7e:7d:da", "192.168.4.107", "192.168.4.107"))
	if err != nil || !gratuitous.Gratuitous {
		t.Fatalf("gratuitous ARP: %+v, %v", gratuitous, err)
	}
}

func TestARPRejectsTruncatedAndMACMismatch(t *testing.T) {
	if _, err := parseARPFrame(make([]byte, 41)); err == nil {
		t.Fatal("truncated frame accepted")
	}
	frame := arpFrame(t, "88:26:3f:7e:7d:da", "192.168.4.107", "192.168.4.1")
	frame[6] = 0x89
	if _, err := parseARPFrame(frame); err == nil {
		t.Fatal("MAC mismatch accepted")
	}
}

func TestMergeUNVResults(t *testing.T) {
	mac := "88:26:3f:7e:7d:da"
	devices := mergeDevices([]DiscoveredDevice{
		{Vendor: "UNV", MAC: mac, IP: net.ParseIP("192.168.4.107"), Protocols: []string{"UNV-ARP"}, Confidence: "high"},
		{Vendor: "UNV", MAC: "88-26-3F-7E-7D-DA", Model: "IPC", Protocols: []string{"UNV-UDP-3705"}, Confidence: "medium"},
		{Vendor: "UNV", MAC: "88:26:3f:8d:65:25", IP: net.ParseIP("192.168.4.106")},
		{Vendor: "Dahua", IP: net.ParseIP("192.168.4.107")},
	})
	if len(devices) != 3 {
		t.Fatalf("got %d devices: %+v", len(devices), devices)
	}
	for _, device := range devices {
		if device.MAC == mac && (device.Model != "IPC" || len(device.Protocols) != 2 || device.Confidence != "high") {
			t.Fatalf("bad merge: %+v", device)
		}
	}
}

type fakeUDPSource struct {
	payload []byte
	sent    bool
	probe   []byte
	dest    *net.UDPAddr
	ifName  string
}

func (s *fakeUDPSource) Send(payload []byte, destination *net.UDPAddr, interfaceName string) error {
	s.probe = append([]byte(nil), payload...)
	s.dest = destination
	s.ifName = interfaceName
	return nil
}
func (s *fakeUDPSource) Read(ctx context.Context, _ time.Time) ([]byte, *net.UDPAddr, string, error) {
	if !s.sent {
		s.sent = true
		probe := string(s.probe)
		start := strings.Index(probe, "<a:MessageID>") + len("<a:MessageID>")
		end := strings.Index(probe, "</a:MessageID>")
		payload := s.payload
		if start >= len("<a:MessageID>") && end > start {
			payload = bytes.Replace(payload, []byte("MESSAGE_ID"), []byte(probe[start:end]), 1)
		}
		return append([]byte(nil), payload...), &net.UDPAddr{IP: net.ParseIP("192.168.4.107")}, "eth0", nil
	}
	return nil, nil, "", context.DeadlineExceeded
}
func (*fakeUDPSource) Close() error { return nil }

type multiUDPSource struct {
	payloads [][]byte
	probe    []byte
	index    int
}

func (s *multiUDPSource) Send(payload []byte, _ *net.UDPAddr, _ string) error {
	s.probe = append([]byte(nil), payload...)
	return nil
}

func (s *multiUDPSource) Read(context.Context, time.Time) ([]byte, *net.UDPAddr, string, error) {
	if s.index >= len(s.payloads) {
		return nil, nil, "", context.DeadlineExceeded
	}
	probe := string(s.probe)
	start := strings.Index(probe, "<a:MessageID>") + len("<a:MessageID>")
	end := strings.Index(probe, "</a:MessageID>")
	payload := bytes.Replace(s.payloads[s.index], []byte("MESSAGE_ID"), []byte(probe[start:end]), 1)
	address := &net.UDPAddr{IP: net.IPv4(192, 168, 4, byte(106+s.index)), Port: 40000 + s.index}
	s.index++
	return payload, address, "eth0", nil
}

func (*multiUDPSource) Close() error { return nil }

func TestUNVListenerKeepsModelsFromSeparateCameraResponses(t *testing.T) {
	response := func(mac, ip, model string) []byte {
		prefix := `<Envelope><Action>x/UniviewProbeMatches</Action><RelatesTo>MESSAGE_ID</RelatesTo><Scopes>onvif://www.onvif.org/macaddr/` + mac + ` onvif://www.onvif.org/name/` + model + `</Scopes><XAddrs>http://` + ip + `/</XAddrs><!--`
		suffix := `--></Envelope>`
		paddingLength := 2369 - len(prefix) - len(suffix)
		if paddingLength < 0 {
			t.Fatal("test UNV response exceeds target datagram size")
		}
		payload := []byte(prefix + strings.Repeat("x", paddingLength) + suffix)
		if len(payload) != 2369 {
			t.Fatalf("test UNV response size = %d", len(payload))
		}
		return payload
	}
	source := &multiUDPSource{payloads: [][]byte{
		response("88263f8d6525", "192.168.4.106", "IPC2124LE-ADF28KM-H"),
		response("88263f7e7dda", "192.168.4.107", "IPC2124LB-ADF28KM-H"),
	}}
	devices, err := listenUNVUDP(context.Background(), source, BackendOptions{InterfaceName: "eth0", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 || devices[0].Model != "IPC2124LE-ADF28KM-H" || devices[1].Model != "IPC2124LB-ADF28KM-H" {
		t.Fatalf("devices = %+v", devices)
	}
}

type fakeARPSource struct {
	frame []byte
	sent  bool
}

func (*fakeARPSource) SendProbe(net.IP) error { return nil }

func (s *fakeARPSource) ReadFrame(context.Context, time.Time) ([]byte, string, error) {
	if s.sent {
		return nil, "", context.DeadlineExceeded
	}
	s.sent = true
	return s.frame, "eth0", nil
}
func (*fakeARPSource) Close() error { return nil }

func TestUDPListenerDoesNotTruncateLargePayload(t *testing.T) {
	padding := strings.Repeat("x", 3000)
	payload := []byte(`<?xml version="1.0"?><Envelope><Header><Action>http://www.uniview.com/ver10/device/wsdl/UniviewProbeMatches</Action><RelatesTo>MESSAGE_ID</RelatesTo></Header><Body><Scopes>onvif://www.onvif.org/name/` + padding + ` onvif://www.onvif.org/mac/88:26:3f:7e:7d:da</Scopes><XAddrs>http://192.168.4.107/onvif/device_service</XAddrs></Body></Envelope>`)
	source := &fakeUDPSource{payload: payload}
	devices, err := listenUNVUDP(context.Background(), source, BackendOptions{Timeout: time.Second, InterfaceName: "eth0"})
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
	if source.dest == nil || source.dest.Port != 3702 || !source.dest.IP.Equal(net.IPv4bcast) {
		t.Fatalf("probe destination = %v", source.dest)
	}
	if source.ifName != "eth0" {
		t.Fatalf("probe interface = %q", source.ifName)
	}
}

func TestUnknownOUIARPIsNotUNV(t *testing.T) {
	devices, err := observeUNVARP(context.Background(), &fakeARPSource{frame: arpFrame(t, "00:11:22:33:44:55", "192.168.4.9", "192.168.4.1")}, BackendOptions{Timeout: time.Second})
	if err != nil || len(devices) != 0 {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
}

func TestARPPermissionFailureKeepsUDPResult(t *testing.T) {
	payload := []byte(`<Envelope><Action>http://www.uniview.com/ver10/device/wsdl/UniviewProbeMatches</Action><RelatesTo>MESSAGE_ID</RelatesTo><Scopes>onvif://www.onvif.org/mac/88:26:3f:7e:7d:da</Scopes><XAddrs>http://192.168.4.107/</XAddrs></Envelope>`)
	backend := UNVDiscoveryBackend{
		UDPFactory: func() (unvDatagramSource, error) { return &fakeUDPSource{payload: payload}, nil },
		ARPFactory: func(string) (arpSource, error) { return nil, errors.New("operation not permitted") },
	}
	devices, err := backend.Discover(context.Background(), BackendOptions{Timeout: time.Second, EnableARP: true})
	if err != nil || len(devices) != 1 {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
}

type cancelUDPSource struct{}

func (*cancelUDPSource) Send([]byte, *net.UDPAddr, string) error { return nil }
func (*cancelUDPSource) Read(ctx context.Context, _ time.Time) ([]byte, *net.UDPAddr, string, error) {
	<-ctx.Done()
	return nil, nil, "", ctx.Err()
}
func (*cancelUDPSource) Close() error { return nil }

func TestUDPListenerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_, err := listenUNVUDP(ctx, &cancelUDPSource{}, BackendOptions{Timeout: time.Minute})
	if !errors.Is(err, context.Canceled) || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("err=%v elapsed=%v", err, time.Since(started))
	}
}

type staticBackend struct {
	name    string
	devices []DiscoveredDevice
	err     error
}

func (b staticBackend) Name() string { return b.name }
func (b staticBackend) Discover(context.Context, BackendOptions) ([]DiscoveredDevice, error) {
	return b.devices, b.err
}

func TestBackendFailureDoesNotDiscardOtherResults(t *testing.T) {
	for _, test := range []struct{ good, bad string }{{"dahua", "unv"}, {"unv", "dahua"}} {
		devices, err := discoverWithBackends(context.Background(), []DiscoveryBackend{
			staticBackend{name: test.good, devices: []DiscoveredDevice{{Vendor: test.good, IP: net.ParseIP("10.0.0.2")}}},
			staticBackend{name: test.bad, err: errors.New("failed")},
		}, BackendOptions{})
		if err != nil || len(devices) != 1 {
			t.Fatalf("%s survives %s failure: devices=%v err=%v", test.good, test.bad, devices, err)
		}
	}
}

func arpFrame(t *testing.T, macText, senderText, targetText string) []byte {
	t.Helper()
	mac, _ := net.ParseMAC(macText)
	sender, target := net.ParseIP(senderText).To4(), net.ParseIP(targetText).To4()
	frame := make([]byte, 42)
	for i := 0; i < 6; i++ {
		frame[i] = 0xff
	}
	copy(frame[6:12], mac)
	binary.BigEndian.PutUint16(frame[12:14], 0x0806)
	binary.BigEndian.PutUint16(frame[14:16], 1)
	binary.BigEndian.PutUint16(frame[16:18], 0x0800)
	frame[18], frame[19] = 6, 4
	binary.BigEndian.PutUint16(frame[20:22], 1)
	copy(frame[22:28], mac)
	copy(frame[28:32], sender)
	copy(frame[38:42], target)
	return frame
}
