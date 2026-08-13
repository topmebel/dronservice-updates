package ipcamera

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

type DiscoveredDevice struct {
	Vendor               string               `json:"vendor"`
	Manufacturer         string               `json:"manufacturer,omitempty"`
	Protocols            []string             `json:"protocols"`
	MAC                  string               `json:"mac,omitempty"`
	IP                   net.IP               `json:"ip,omitempty"`
	SubnetMask           net.IPMask           `json:"subnetMask,omitempty"`
	Gateway              net.IP               `json:"gateway,omitempty"`
	Model                string               `json:"model,omitempty"`
	SerialNumber         string               `json:"serialNumber,omitempty"`
	FirmwareVersion      string               `json:"firmwareVersion,omitempty"`
	DeviceType           string               `json:"deviceType,omitempty"`
	DeviceName           string               `json:"deviceName,omitempty"`
	HTTPPort             uint16               `json:"httpPort,omitempty"`
	RTSPPort             uint16               `json:"rtspPort,omitempty"`
	ServicePort          uint16               `json:"servicePort,omitempty"`
	InterfaceName        string               `json:"interfaceName,omitempty"`
	SourceAddress        *net.UDPAddr         `json:"sourceAddress,omitempty"`
	Confidence           string               `json:"confidence,omitempty"`
	RawFields            map[string]any       `json:"rawFields,omitempty"`
	InitializationStatus InitializationStatus `json:"initializationStatus"`
}

type DiscoverOptions struct {
	InterfaceName string
	Timeout       time.Duration
	Vendors       []string
	EnableARP     bool
	Verbose       bool
}

type BackendOptions struct {
	InterfaceName string
	Timeout       time.Duration
	EnableARP     bool
	Verbose       bool
}

type DiscoveryBackend interface {
	Name() string
	Discover(context.Context, BackendOptions) ([]DiscoveredDevice, error)
}

type UNVActiveProbe interface {
	SendProbe(context.Context, net.PacketConn, *net.Interface) error
}

func DiscoverCameras(ctx context.Context, opts DiscoverOptions) ([]DiscoveredDevice, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if len(opts.Vendors) == 0 {
		opts.Vendors = []string{"dahua", "unv"}
		opts.EnableARP = true
	}
	backends := make([]DiscoveryBackend, 0, 2)
	for _, vendor := range opts.Vendors {
		switch strings.ToLower(vendor) {
		case "all":
			backends = []DiscoveryBackend{DahuaDiscoveryBackend{}, UNVDiscoveryBackend{}}
			goto selected
		case "dahua":
			backends = append(backends, DahuaDiscoveryBackend{})
		case "unv", "uniview":
			backends = append(backends, UNVDiscoveryBackend{})
		}
	}
selected:
	if len(backends) == 0 {
		return nil, fmt.Errorf("no supported discovery vendors selected")
	}
	return discoverWithBackends(ctx, backends, BackendOptions{InterfaceName: opts.InterfaceName, Timeout: opts.Timeout, EnableARP: opts.EnableARP, Verbose: opts.Verbose})
}

func discoverWithBackends(ctx context.Context, backends []DiscoveryBackend, opts BackendOptions) ([]DiscoveredDevice, error) {
	type result struct {
		devices []DiscoveredDevice
		err     error
		name    string
	}
	results := make(chan result, len(backends))
	var wg sync.WaitGroup
	for _, backend := range backends {
		wg.Add(1)
		go func(b DiscoveryBackend) {
			defer wg.Done()
			d, e := b.Discover(ctx, opts)
			results <- result{d, e, b.Name()}
		}(backend)
	}
	wg.Wait()
	close(results)
	all := make([]DiscoveredDevice, 0)
	errs := make([]error, 0)
	for result := range results {
		if result.err != nil {
			log.Printf("camera discovery backend=%s: %v", result.name, result.err)
			errs = append(errs, fmt.Errorf("%s: %w", result.name, result.err))
		}
		all = append(all, result.devices...)
	}
	if len(all) == 0 && len(errs) == len(backends) {
		return nil, errors.Join(errs...)
	}
	return mergeDevices(all), nil
}

func mergeDevices(devices []DiscoveredDevice) []DiscoveredDevice {
	merged := make([]DiscoveredDevice, 0, len(devices))
	for _, device := range devices {
		device.MAC = normalizeMAC(device.MAC)
		index := -1
		for i := range merged {
			if sameDevice(merged[i], device) {
				index = i
				break
			}
		}
		if index < 0 {
			device.Protocols = uniqueStrings(device.Protocols)
			merged = append(merged, device)
			continue
		}
		merged[index] = mergeDevice(merged[index], device)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].IP.String() < merged[j].IP.String() })
	return merged
}

func sameDevice(a, b DiscoveredDevice) bool {
	if a.Vendor != b.Vendor {
		return false
	}
	if a.MAC != "" && b.MAC != "" {
		return a.MAC == b.MAC
	}
	if a.SerialNumber != "" && b.SerialNumber != "" {
		return a.SerialNumber == b.SerialNumber
	}
	if a.IP != nil && b.IP != nil && a.IP.Equal(b.IP) && a.Model != "" && b.Model != "" {
		return a.Model == b.Model
	}
	return a.SourceAddress != nil && b.SourceAddress != nil && a.SourceAddress.IP.Equal(b.SourceAddress.IP)
}

func mergeDevice(a, b DiscoveredDevice) DiscoveredDevice {
	a.Protocols = uniqueStrings(append(a.Protocols, b.Protocols...))
	if confidenceRank(b.Confidence) > confidenceRank(a.Confidence) {
		a.Confidence = b.Confidence
	}
	if a.MAC == "" {
		a.MAC = b.MAC
	}
	if a.Manufacturer == "" {
		a.Manufacturer = b.Manufacturer
	}
	if a.IP == nil {
		a.IP = b.IP
	}
	if a.SubnetMask == nil {
		a.SubnetMask = b.SubnetMask
	}
	if a.Gateway == nil {
		a.Gateway = b.Gateway
	}
	if a.Model == "" {
		a.Model = b.Model
	}
	if a.SerialNumber == "" {
		a.SerialNumber = b.SerialNumber
	}
	if a.FirmwareVersion == "" {
		a.FirmwareVersion = b.FirmwareVersion
	}
	if a.DeviceType == "" {
		a.DeviceType = b.DeviceType
	}
	if a.DeviceName == "" {
		a.DeviceName = b.DeviceName
	}
	if a.HTTPPort == 0 {
		a.HTTPPort = b.HTTPPort
	}
	if a.RTSPPort == 0 {
		a.RTSPPort = b.RTSPPort
	}
	if a.ServicePort == 0 {
		a.ServicePort = b.ServicePort
	}
	if a.InterfaceName == "" {
		a.InterfaceName = b.InterfaceName
	}
	if a.SourceAddress == nil {
		a.SourceAddress = b.SourceAddress
	}
	a.InitializationStatus = mergeInitializationStatus(a.InitializationStatus, b.InitializationStatus)
	if a.RawFields == nil {
		a.RawFields = map[string]any{}
	}
	for key, value := range b.RawFields {
		if _, ok := a.RawFields[key]; !ok {
			a.RawFields[key] = value
		}
	}
	return a
}

func mergeInitializationStatus(a, b InitializationStatus) InitializationStatus {
	a = normalizeInitializationStatus(a)
	b = normalizeInitializationStatus(b)
	if a == InitializationUnknown {
		return b
	}
	if b == InitializationUnknown {
		return a
	}
	if a != b {
		return InitializationUnknown
	}
	return a
}
func confidenceRank(value string) int {
	switch value {
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}
func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
