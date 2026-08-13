package ipcamera

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	macPattern       = regexp.MustCompile(`(?i)(?:[0-9a-f]{2}[:-]){5}[0-9a-f]{2}|\b[0-9a-f]{12}\b`)
	ipPattern        = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	printablePattern = regexp.MustCompile(`[\x20-\x7e]{4,}`)
)
var univiewOUIs = map[[3]byte]string{{0x88, 0x26, 0x3f}: "Uniview"}

func ParseUNVAnnouncement(payload []byte, source *net.UDPAddr, interfaceName string) (*DiscoveredDevice, error) {
	if len(payload) == 0 || len(payload) > 65535 {
		return nil, fmt.Errorf("invalid UNV payload length %d", len(payload))
	}
	stringsFound := safePrintableStrings(payload)
	fields := extractStructuredFields(payload)
	mac := findMAC(payload, stringsFound)
	knownOUI := isUNVOUI(mac)
	evidence := knownOUI || containsUNVMarker(stringsFound)
	if !evidence && (len(payload) < 512 || len(stringsFound) == 0) {
		return nil, fmt.Errorf("payload has no UNV evidence")
	}
	device := &DiscoveredDevice{Vendor: "UNV", Protocols: []string{"UNV-UDP-3705"}, MAC: mac, InterfaceName: interfaceName, SourceAddress: source, Confidence: "medium", RawFields: fields}
	if knownOUI {
		device.Confidence = "high"
	}
	if source != nil {
		device.IP = append(net.IP(nil), source.IP...)
	}
	applyHeuristicFields(device, fields, stringsFound)
	if device.IP == nil {
		return nil, fmt.Errorf("UNV announcement has no source or reported IP")
	}
	return device, nil
}

func findMAC(payload []byte, values []string) string {
	for _, value := range values {
		if match := macPattern.FindString(value); match != "" {
			return normalizeMAC(match)
		}
	}
	for index := 0; index+6 <= len(payload); index++ {
		candidate := payload[index : index+6]
		if _, ok := univiewOUIs[[3]byte{candidate[0], candidate[1], candidate[2]}]; ok {
			return net.HardwareAddr(candidate).String()
		}
	}
	return ""
}
func isUNVOUI(mac string) bool {
	parsed, err := net.ParseMAC(mac)
	if err != nil || len(parsed) < 3 {
		return false
	}
	_, ok := univiewOUIs[[3]byte{parsed[0], parsed[1], parsed[2]}]
	return ok
}
func containsUNVMarker(values []string) bool {
	for _, value := range values {
		lower := strings.ToLower(value)
		if strings.Contains(lower, "uniview") || strings.Contains(lower, "unv") || strings.Contains(lower, "ipc") {
			return true
		}
	}
	return false
}

func safePrintableStrings(payload []byte) []string {
	values := make([]string, 0)
	for _, value := range printablePattern.FindAll(payload, 20) {
		trimmed := strings.TrimSpace(string(value))
		if trimmed != "" && utf8.ValidString(trimmed) {
			if len(trimmed) > 160 {
				trimmed = trimmed[:160]
			}
			values = append(values, trimmed)
		}
	}
	return values
}
func extractStructuredFields(payload []byte) map[string]any {
	fields := map[string]any{}
	start, end := bytes.IndexByte(payload, '{'), bytes.LastIndexByte(payload, '}')
	if start >= 0 && end > start {
		var value any
		if json.Unmarshal(payload[start:end+1], &value) == nil {
			flattenValue("", value, fields)
		}
	}
	xmlStart := bytes.IndexByte(payload, '<')
	if xmlStart >= 0 {
		decoder := xml.NewDecoder(bytes.NewReader(payload[xmlStart:]))
		var current string
		for {
			token, err := decoder.Token()
			if err != nil {
				break
			}
			switch item := token.(type) {
			case xml.StartElement:
				current = item.Name.Local
			case xml.CharData:
				text := strings.TrimSpace(string(item))
				if current != "" && text != "" && len(text) < 256 {
					fields[current] = text
				}
			}
		}
	}
	return fields
}
func flattenValue(prefix string, value any, fields map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			name := key
			if prefix != "" {
				name = prefix + "." + key
			}
			flattenValue(name, item, fields)
		}
	case string, float64, bool:
		if prefix != "" {
			fields[prefix] = typed
		}
	}
}
func applyHeuristicFields(device *DiscoveredDevice, fields map[string]any, stringsFound []string) {
	for key, value := range fields {
		text := fmt.Sprint(value)
		lower := strings.ToLower(key)
		switch {
		case strings.Contains(lower, "mac") && device.MAC == "":
			device.MAC = normalizeMAC(text)
		case strings.Contains(lower, "mask"):
			if ip := net.ParseIP(text).To4(); ip != nil {
				device.SubnetMask = net.IPMask(ip)
			}
		case strings.Contains(lower, "gateway"):
			device.Gateway = net.ParseIP(text).To4()
		case strings.Contains(lower, "ip") && !strings.Contains(lower, "mask"):
			if ip := net.ParseIP(text).To4(); ip != nil {
				device.IP = ip
			}
		case strings.Contains(lower, "model") || strings.Contains(lower, "product"):
			device.Model = text
		case strings.Contains(lower, "serial"):
			device.SerialNumber = text
		case strings.Contains(lower, "firmware") || strings.Contains(lower, "version"):
			device.FirmwareVersion = text
		case strings.Contains(lower, "name"):
			device.DeviceName = text
		}
	}
	if device.MAC == "" {
		for _, value := range stringsFound {
			if match := macPattern.FindString(value); match != "" {
				device.MAC = normalizeMAC(match)
				break
			}
		}
	}
	if device.IP == nil {
		for _, value := range stringsFound {
			for _, candidate := range ipPattern.FindAllString(value, -1) {
				if ip := net.ParseIP(candidate).To4(); ip != nil {
					device.IP = ip
					return
				}
			}
		}
	}
}
func payloadDebugSummary(payload []byte) map[string]any {
	sum := sha256.Sum256(payload)
	prefix := payload
	if len(prefix) > 32 {
		prefix = prefix[:32]
	}
	return map[string]any{"size": len(payload), "prefix": hex.EncodeToString(prefix), "sha256": hex.EncodeToString(sum[:]), "strings": safePrintableStrings(payload)}
}
