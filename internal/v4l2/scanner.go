package v4l2

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultSysClassPath = "/sys/class/video4linux"
	defaultDevicePath   = "/dev"
)

type Device struct {
	ID                 string   `json:"id"`
	Path               string   `json:"path"`
	Name               string   `json:"name"`
	Driver             string   `json:"driver,omitempty"`
	Card               string   `json:"card,omitempty"`
	Bus                string   `json:"bus,omitempty"`
	Version            string   `json:"version,omitempty"`
	Capabilities       []string `json:"capabilities"`
	Formats            []Format `json:"formats"`
	ConfiguredName     string   `json:"configuredName,omitempty"`
	SelectedFormat     string   `json:"selectedFormat,omitempty"`
	SelectedResolution string   `json:"selectedResolution,omitempty"`
	SelectedFPS        string   `json:"selectedFps,omitempty"`
	Use                bool     `json:"use"`
	Error              string   `json:"error,omitempty"`
}

type Format struct {
	PixelFormat string `json:"pixelFormat"`
	Description string `json:"description"`
	Modes       []Mode `json:"modes"`
}

type Mode struct {
	Resolution string `json:"resolution"`
	FPS        string `json:"fps"`
}

type ListResponse struct {
	Devices []Device `json:"devices"`
}

type Scanner struct {
	sysClassPath string
	devicePath   string
}

func NewScanner() *Scanner {
	return &Scanner{
		sysClassPath: defaultSysClassPath,
		devicePath:   defaultDevicePath,
	}
}

func (s *Scanner) Scan(ctx context.Context) ([]Device, error) {
	entries, err := os.ReadDir(s.sysClassPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Device{}, nil
		}
		return nil, fmt.Errorf("read V4L2 device directory: %w", err)
	}

	devices := make([]Device, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("scan V4L2 devices: %w", err)
		}

		if !strings.HasPrefix(entry.Name(), "video") {
			continue
		}

		device := Device{
			Path:         filepath.Join(s.devicePath, entry.Name()),
			Name:         readName(filepath.Join(s.sysClassPath, entry.Name(), "name"), entry.Name()),
			Capabilities: []string{},
		}

		capability, err := queryCapability(device.Path)
		if err != nil {
			continue
		}
		if !isUSBVideoCapture(capability) {
			continue
		}

		device.Driver = capability.Driver
		device.Card = capability.Card
		device.Bus = capability.Bus
		device.ID = capability.Bus
		device.Version = formatVersion(capability.Version)
		device.Capabilities = capabilityNames(capability.Capabilities)
		device.Formats, err = queryFormats(device.Path)
		if err != nil {
			device.Error = err.Error()
			device.Formats = []Format{}
		}

		devices = append(devices, device)
	}

	sort.Slice(devices, func(i, j int) bool { return devices[i].Path < devices[j].Path })
	return devices, nil
}

func isUSBVideoCapture(capability capability) bool {
	const (
		videoCapture            = 0x00000001
		videoCaptureMultiplanar = 0x00001000
	)

	isUSB := strings.HasPrefix(strings.ToLower(strings.TrimSpace(capability.Bus)), "usb-")
	isCapture := capability.Capabilities&(videoCapture|videoCaptureMultiplanar) != 0
	return isUSB && isCapture
}

func readName(path, fallback string) string {
	value, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}
	return strings.TrimSpace(string(value))
}

func formatVersion(version uint32) string {
	return fmt.Sprintf("%d.%d.%d", version>>16, (version>>8)&0xff, version&0xff)
}

func capabilityNames(capabilities uint32) []string {
	known := []struct {
		mask uint32
		name string
	}{
		{0x00000001, "video capture"},
		{0x00001000, "video capture multiplanar"},
		{0x00010000, "tuner"},
		{0x00800000, "metadata capture"},
		{0x01000000, "read/write"},
		{0x04000000, "streaming"},
	}

	names := make([]string, 0, len(known))
	for _, item := range known {
		if capabilities&item.mask != 0 {
			names = append(names, item.name)
		}
	}
	return names
}
