package ipcamera

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
)

const (
	unvProbeAction        = "http://www.uniview.com/Ver10/Device/wsdl/UniviewProbe"
	unvProbeMatchesSuffix = "/UniviewProbeMatches"
)

func buildUNVProbe(messageID string) []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:tns="http://www.uniview.com/Ver10/Device/wsdl">` +
		`<s:Header><a:Action>` + unvProbeAction + `</a:Action><a:MessageID>` + messageID + `</a:MessageID>` +
		`<a:ReplyTo><a:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:Address></a:ReplyTo>` +
		`<a:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</a:To></s:Header><s:Body><tns:UniviewProbe/></s:Body></s:Envelope>`)
}

type unvProbeMatch struct {
	Action    string
	RelatesTo string
	Scopes    string
	XAddrs    string
}

func ParseUNVProbeMatch(payload []byte, source *net.UDPAddr, interfaceName, messageID string) (*DiscoveredDevice, error) {
	devices, err := ParseUNVProbeMatches(payload, source, interfaceName, messageID)
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("UNV ProbeMatch contains no devices")
	}
	return &devices[0], nil
}

func ParseUNVProbeMatches(payload []byte, source *net.UDPAddr, interfaceName, messageID string) ([]DiscoveredDevice, error) {
	match, err := decodeUNVProbeMatch(payload)
	if err != nil {
		return nil, err
	}
	if !isUNVProbeResponseAction(match.Action) {
		return nil, fmt.Errorf("unexpected UNV Action %q", match.Action)
	}
	if strings.TrimSpace(match.RelatesTo) == "" {
		if strings.TrimSpace(match.Action) != unvProbeAction {
			return nil, fmt.Errorf("UNV response has no RelatesTo")
		}
	} else if !sameUNVMessageID(match.RelatesTo, messageID) {
		return nil, fmt.Errorf("UNV RelatesTo %q does not match request MessageID %q", strings.TrimSpace(match.RelatesTo), messageID)
	}
	records, err := decodeUNVDeviceRecords(payload)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		records = []unvDeviceRecord{{Scopes: match.Scopes, XAddrs: match.XAddrs}}
	}
	devices := make([]DiscoveredDevice, 0, len(records))
	for _, record := range records {
		device, err := newUNVDiscoveredDevice(record, match, source, interfaceName)
		if err != nil {
			continue
		}
		devices = append(devices, *device)
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("UNV ProbeMatch has no device with an IP address")
	}
	return mergeDevices(devices), nil
}

func isUNVProbeResponseAction(action string) bool {
	action = strings.TrimSpace(action)
	return action == unvProbeAction || strings.HasSuffix(action, unvProbeMatchesSuffix)
}

type unvDeviceRecord struct {
	Scopes string `xml:"Scopes"`
	XAddrs string `xml:"XAddrs"`
}

func newUNVDiscoveredDevice(record unvDeviceRecord, match unvProbeMatch, source *net.UDPAddr, interfaceName string) (*DiscoveredDevice, error) {
	fields := map[string]any{"action": match.Action, "relatesTo": match.RelatesTo, "scopes": record.Scopes, "xAddrs": record.XAddrs}
	stringsFound := []string{record.Scopes, record.XAddrs}
	device := &DiscoveredDevice{Vendor: "UNV", Protocols: []string{"UNV-UDP-3702"}, InterfaceName: interfaceName, SourceAddress: source, Confidence: "medium", RawFields: fields}
	device.MAC = findMAC([]byte(record.Scopes), stringsFound)
	if isUNVOUI(device.MAC) {
		device.Confidence = "high"
	}
	applyXAddrFields(device, record.XAddrs)
	if device.IP == nil && source != nil {
		device.IP = append(net.IP(nil), source.IP...)
	}
	applyScopeFields(device, record.Scopes)
	device.DeviceName = unvDeviceNameFromScopes(record.Scopes)
	if device.Model == "" && looksLikeUNVModel(device.DeviceName) {
		device.Model = device.DeviceName
	}
	if device.IP == nil {
		return nil, fmt.Errorf("UNV ProbeMatch has no IP address")
	}
	return device, nil
}

func looksLikeUNVModel(value string) bool {
	upper := strings.ToUpper(strings.TrimSpace(value))
	for _, prefix := range []string{"IPC", "NVR", "XVR", "DVR"} {
		if strings.HasPrefix(upper, prefix) && len(upper) > len(prefix) {
			return true
		}
	}
	return false
}

func decodeUNVDeviceRecords(payload []byte) ([]unvDeviceRecord, error) {
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	records := make([]unvDeviceRecord, 0)
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return records, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode UNV device matches: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "UniviewProbeMatch" {
			continue
		}
		var record unvDeviceRecord
		if err := decoder.DecodeElement(&record, &start); err != nil {
			return nil, fmt.Errorf("decode UNV device match: %w", err)
		}
		records = append(records, record)
	}
}

func sameUNVMessageID(left, right string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(value)
		value = strings.TrimPrefix(strings.ToLower(value), "urn:")
		return value
	}
	return normalize(left) != "" && normalize(left) == normalize(right)
}

func decodeUNVProbeMatch(payload []byte) (unvProbeMatch, error) {
	if len(payload) == 0 || len(payload) > 65535 {
		return unvProbeMatch{}, fmt.Errorf("invalid UNV XML length %d", len(payload))
	}
	decoder := xml.NewDecoder(bytes.NewReader(payload))
	var result unvProbeMatch
	for {
		token, err := decoder.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			return unvProbeMatch{}, fmt.Errorf("decode UNV XML: %w", err)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		var target *string
		switch start.Name.Local {
		case "Action":
			target = &result.Action
		case "RelatesTo":
			target = &result.RelatesTo
		case "Scopes":
			target = &result.Scopes
		case "XAddrs":
			target = &result.XAddrs
		}
		if target != nil {
			if err := decoder.DecodeElement(target, &start); err != nil {
				return unvProbeMatch{}, fmt.Errorf("decode UNV %s: %w", start.Name.Local, err)
			}
		}
	}
	return result, nil
}

func applyScopeFields(device *DiscoveredDevice, scopes string) {
	for _, scope := range strings.Fields(scopes) {
		parsed, err := url.Parse(scope)
		if err != nil || !strings.EqualFold(parsed.Scheme, "onvif") || !strings.EqualFold(parsed.Host, "www.onvif.org") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(parsed.EscapedPath(), "/"), "/", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(parts[0])
		value, err := url.PathUnescape(parts[1])
		if err != nil || value == "" {
			continue
		}
		switch key {
		case "hardware", "model":
			device.Model = value
		case "manufacturer":
			device.Manufacturer = value
		case "type":
			device.DeviceType = value
		case "serial":
			device.SerialNumber = value
		case "firmware", "version":
			device.FirmwareVersion = value
		case "mac", "macaddr":
			if mac := normalizeMAC(value); mac != "" {
				device.MAC = mac
			}
		}
	}
}

func unvDeviceNameFromScopes(scopes string) string {
	for _, scope := range strings.Fields(scopes) {
		parsed, err := url.Parse(scope)
		if err != nil || !strings.EqualFold(parsed.Scheme, "onvif") || !strings.EqualFold(parsed.Host, "www.onvif.org") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(parsed.EscapedPath(), "/"), "/", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "name") {
			continue
		}
		name, err := url.PathUnescape(parts[1])
		if err == nil && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

func applyXAddrFields(device *DiscoveredDevice, xaddrs string) {
	for _, address := range strings.Fields(xaddrs) {
		parsed, err := url.Parse(address)
		if err != nil {
			continue
		}
		ip := net.ParseIP(parsed.Hostname()).To4()
		if ip == nil {
			continue
		}
		if device.IP == nil {
			device.IP = ip
		}
		if device.HTTPPort == 0 && (strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) {
			port := 80
			if strings.EqualFold(parsed.Scheme, "https") {
				port = 443
			}
			if parsed.Port() != "" {
				parsedPort, parseErr := strconv.Atoi(parsed.Port())
				if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
					continue
				}
				port = parsedPort
			}
			device.HTTPPort = uint16(port)
		}
	}
}
