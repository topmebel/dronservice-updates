package ipcamera

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

const dahuaRequestJSON = `{"method":"DHDiscover.search","params":{"mac":"","uni":1}}`

var legacyRequest = [32]byte{0xa3, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02}

type DahuaDevice struct {
	MAC                  string
	IP                   net.IP
	SubnetMask           net.IPMask
	Gateway              net.IP
	Model                string
	DeviceClass          string
	SerialNumber         string
	FirmwareVersion      string
	MachineName          string
	Manufacturer         string
	Vendor               string
	HTTPPort             uint16
	ServicePort          uint16
	DHCPEnabled          *bool
	Protocol             string
	SourceAddress        *net.UDPAddr
	InitializationStatus InitializationStatus
}

func buildDHIPRequest() []byte {
	jsonBytes := []byte(dahuaRequestJSON)
	packet := make([]byte, 32+len(jsonBytes))
	packet[0] = 0x20
	copy(packet[4:8], "DHIP")
	binary.LittleEndian.PutUint32(packet[16:20], uint32(len(jsonBytes)))
	binary.LittleEndian.PutUint32(packet[24:28], uint32(len(jsonBytes)))
	copy(packet[32:], jsonBytes)
	return packet
}

type dahuaResponse struct {
	MAC    string `json:"mac"`
	Method string `json:"method"`
	Params struct {
		DeviceInfo dahuaDeviceInfo `json:"deviceInfo"`
	} `json:"params"`
}

type dahuaDeviceInfo struct {
	DeviceClass string          `json:"DeviceClass"`
	DeviceType  string          `json:"DeviceType"`
	HTTPPort    int             `json:"HttpPort"`
	Port        int             `json:"Port"`
	Init        json.RawMessage `json:"Init"`
	IPv4        struct {
		IPAddress      string `json:"IPAddress"`
		SubnetMask     string `json:"SubnetMask"`
		DefaultGateway string `json:"DefaultGateway"`
		DHCPEnabled    *bool  `json:"DhcpEnable"`
	} `json:"IPv4Address"`
	SerialNumber string `json:"SerialNo"`
	Version      string `json:"Version"`
	MachineName  string `json:"MachineName"`
	Manufacturer string `json:"Manufacturer"`
	Vendor       string `json:"Vendor"`
}

// isCameraInitialized interprets only explicit Dahua values. Missing, numeric,
// or firmware-specific values remain unknown instead of being guessed.
func isCameraInitialized(info dahuaDeviceInfo) InitializationStatus {
	if len(info.Init) == 0 {
		return InitializationUnknown
	}
	var value any
	if err := json.Unmarshal(info.Init, &value); err != nil {
		return InitializationUnknown
	}
	switch value := value.(type) {
	case bool:
		if value {
			return InitializationCompleted
		}
		return InitializationRequired
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "initialized":
			return InitializationCompleted
		case "false", "uninitialized":
			return InitializationRequired
		}
	}
	return InitializationUnknown
}

func parseDahuaPacket(packet []byte, source *net.UDPAddr) (DahuaDevice, error) {
	if len(packet) >= 8 && bytes.Equal(packet[4:8], []byte("DHIP")) && len(packet) < 32 {
		return DahuaDevice{}, fmt.Errorf("truncated DHIP header")
	}
	start, end := bytes.IndexByte(packet, '{'), bytes.LastIndexByte(packet, '}')
	if start < 0 || end < start {
		return DahuaDevice{}, fmt.Errorf("JSON payload not found")
	}
	var response dahuaResponse
	if err := json.Unmarshal(packet[start:end+1], &response); err != nil {
		return DahuaDevice{}, fmt.Errorf("decode Dahua response: %w", err)
	}
	if response.Method != "client.notifyDevInfo" {
		return DahuaDevice{}, fmt.Errorf("unexpected Dahua method %q", response.Method)
	}
	info := response.Params.DeviceInfo
	ip := net.ParseIP(info.IPv4.IPAddress).To4()
	if ip == nil {
		return DahuaDevice{}, fmt.Errorf("invalid reported IPv4 address")
	}
	maskIP := net.ParseIP(info.IPv4.SubnetMask).To4()
	var mask net.IPMask
	if maskIP != nil {
		mask = net.IPMask(maskIP)
	}
	return DahuaDevice{
		MAC: normalizeMAC(response.MAC), IP: ip, SubnetMask: mask, Gateway: net.ParseIP(info.IPv4.DefaultGateway).To4(),
		Model: info.DeviceType, DeviceClass: info.DeviceClass, SerialNumber: info.SerialNumber,
		FirmwareVersion: info.Version, MachineName: info.MachineName, Manufacturer: info.Manufacturer,
		Vendor: info.Vendor, HTTPPort: boundedPort(info.HTTPPort), ServicePort: boundedPort(info.Port),
		DHCPEnabled: info.IPv4.DHCPEnabled, Protocol: "DHIP", SourceAddress: source,
		InitializationStatus: isCameraInitialized(info),
	}, nil
}

func boundedPort(port int) uint16 {
	if port < 0 || port > 65535 {
		return 0
	}
	return uint16(port)
}
func normalizeMAC(value string) string {
	value = strings.TrimSpace(value)
	compact := strings.NewReplacer(":", "", "-", "", ".", "").Replace(value)
	if len(compact) != 12 {
		return ""
	}
	parsed, err := net.ParseMAC(compact)
	if err != nil || len(parsed) != 6 {
		return ""
	}
	return strings.ToLower(parsed.String())
}
func deviceKey(device DahuaDevice) string {
	if device.MAC != "" {
		return "mac:" + normalizeMAC(device.MAC)
	}
	return "ip:" + device.IP.String() + "|serial:" + device.SerialNumber
}
