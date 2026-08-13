package v4l2

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScannerExcludesDeviceWhenCapabilitiesCannotBeRead(t *testing.T) {
	root := t.TempDir()
	sysClass := filepath.Join(root, "video4linux")
	deviceDir := filepath.Join(root, "dev")
	if err := os.MkdirAll(filepath.Join(sysClass, "video2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deviceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sysClass, "video2", "name"), []byte("USB Video\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	scanner := &Scanner{sysClassPath: sysClass, devicePath: deviceDir}
	devices, err := scanner.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(devices) != 0 {
		t.Fatalf("devices = %#v, want an unverified device to be excluded", devices)
	}
}

func TestIsUSBVideoCapture(t *testing.T) {
	tests := []struct {
		name       string
		capability capability
		want       bool
	}{
		{name: "USB capture", capability: capability{Bus: "usb-xhci-hcd.1-1", Capabilities: 0x00000001}, want: true},
		{name: "USB multiplanar capture", capability: capability{Bus: "usb-xhci-hcd.1-1", Capabilities: 0x00001000}, want: true},
		{name: "USB metadata", capability: capability{Bus: "usb-xhci-hcd.1-1", Capabilities: 0x00800000}, want: false},
		{name: "platform capture", capability: capability{Bus: "platform:camera", Capabilities: 0x00000001}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isUSBVideoCapture(tt.capability); got != tt.want {
				t.Fatalf("isUSBVideoCapture() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCapabilityNames(t *testing.T) {
	got := capabilityNames(0x00000001 | 0x04000000)
	want := []string{"video capture", "streaming"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilityNames() = %#v, want %#v", got, want)
	}
}

func TestFormatVersion(t *testing.T) {
	if got := formatVersion(0x00060a03); got != "6.10.3" {
		t.Fatalf("formatVersion() = %q, want 6.10.3", got)
	}
}
