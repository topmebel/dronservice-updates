package ipcamera

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"testing"
)

func TestBuildDHIPRequestHeader(t *testing.T) {
	packet := buildDHIPRequest()
	want := make([]byte, 32)
	want[0] = 0x20
	copy(want[4:8], "DHIP")
	binary.LittleEndian.PutUint32(want[16:20], uint32(len(dahuaRequestJSON)))
	binary.LittleEndian.PutUint32(want[24:28], uint32(len(dahuaRequestJSON)))
	if !bytes.Equal(packet[:32], want) {
		t.Fatalf("header = %x, want %x", packet[:32], want)
	}
	if got := binary.LittleEndian.Uint32(packet[16:20]); got != uint32(len(packet)-32) {
		t.Fatalf("first JSON length = %d, want %d", got, len(packet)-32)
	}
	if got := binary.LittleEndian.Uint32(packet[24:28]); got != uint32(len(packet)-32) {
		t.Fatalf("second JSON length = %d, want %d", got, len(packet)-32)
	}
}

func TestParseDahuaPacket(t *testing.T) {
	packet := syntheticDahuaResponse(nil)
	device, err := parseDahuaPacket(packet, &net.UDPAddr{IP: net.ParseIP("192.168.1.50"), Port: 37810})
	if err != nil {
		t.Fatalf("parseDahuaPacket() error = %v", err)
	}
	if device.MAC != "40:7a:a4:e8:9b:e9" || !device.IP.Equal(net.ParseIP("192.168.106.108")) || device.Model != "IPC-HFW" || device.SerialNumber != "ABC123" || device.HTTPPort != 80 || device.ServicePort != 37777 {
		t.Fatalf("device = %#v", device)
	}
	if device.InitializationStatus != InitializationUnknown {
		t.Fatalf("initialization status = %q, want unknown for fixture without Init", device.InitializationStatus)
	}
}

func TestIsCameraInitializedUsesOnlyExplicitValues(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want InitializationStatus
	}{
		{name: "missing", want: InitializationUnknown},
		{name: "boolean initialized", raw: `true`, want: InitializationCompleted},
		{name: "boolean uninitialized", raw: `false`, want: InitializationRequired},
		{name: "string initialized", raw: `"initialized"`, want: InitializationCompleted},
		{name: "string uninitialized", raw: `"uninitialized"`, want: InitializationRequired},
		{name: "unknown numeric vendor value", raw: `1`, want: InitializationUnknown},
		{name: "unknown text", raw: `"ready"`, want: InitializationUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := dahuaDeviceInfo{}
			if tt.raw != "" {
				info.Init = json.RawMessage(tt.raw)
			}
			if got := isCameraInitialized(info); got != tt.want {
				t.Fatalf("isCameraInitialized(%s) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseDahuaPacketWithTrailingBytes(t *testing.T) {
	packet := syntheticDahuaResponse([]byte("\n\x00\x00"))
	if _, err := parseDahuaPacket(packet, nil); err != nil {
		t.Fatalf("parseDahuaPacket() error = %v", err)
	}
}

func TestParseDahuaPacketRejectsInvalidPacket(t *testing.T) {
	for _, packet := range [][]byte{[]byte("not Dahua"), []byte(`{"method":"other"}`), {0x20, 0, 0, 0, 'D', 'H', 'I', 'P'}} {
		if _, err := parseDahuaPacket(packet, nil); err == nil {
			t.Fatalf("parseDahuaPacket(%q) error = nil", packet)
		}
	}
}

func TestDeduplicateDevices(t *testing.T) {
	devices := []DahuaDevice{
		{MAC: "40:7A:A4:E8:9B:E9", IP: net.ParseIP("192.168.106.108")},
		{MAC: "40-7a-a4-e8-9b-e9", IP: net.ParseIP("192.168.106.109")},
		{IP: net.ParseIP("192.168.106.110"), SerialNumber: "XYZ"},
		{IP: net.ParseIP("192.168.106.110"), SerialNumber: "XYZ"},
	}
	if got := len(deduplicateDevices(devices)); got != 2 {
		t.Fatalf("deduplicated count = %d, want 2", got)
	}
}

func TestLegacyRequest(t *testing.T) {
	want := []byte{0xa3, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(legacyRequest[:], want) {
		t.Fatalf("legacy request = %x, want %x", legacyRequest, want)
	}
}

func syntheticDahuaResponse(trailing []byte) []byte {
	payload := []byte(`{"mac":"40-7A-A4-E8-9B-E9","method":"client.notifyDevInfo","params":{"deviceInfo":{"DeviceClass":"IPC","DeviceType":"IPC-HFW","HttpPort":80,"Port":37777,"IPv4Address":{"IPAddress":"192.168.106.108","SubnetMask":"255.255.255.0","DefaultGateway":"192.168.106.1","DhcpEnable":false},"SerialNo":"ABC123","Version":"V2.0","MachineName":"Camera","Manufacturer":"Dahua","Vendor":"Dahua"}}}`)
	packet := make([]byte, 32+len(payload)+len(trailing))
	packet[0] = 0x20
	copy(packet[4:8], "DHIP")
	binary.LittleEndian.PutUint32(packet[16:20], uint32(len(payload)))
	binary.LittleEndian.PutUint32(packet[24:28], uint32(len(payload)))
	copy(packet[32:], payload)
	copy(packet[32+len(payload):], trailing)
	return packet
}
