package deviceconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStorePersistsConfiguration(t *testing.T) {
	directory := t.TempDir()
	store, err := NewStore(directory)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	want := Config{
		DeviceID:    "usb-xhci-hcd.1-1",
		Name:        "Камера 1",
		PixelFormat: "MJPG",
		Resolution:  "1920x1080",
		FPS:         "30",
	}
	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded, err := NewStore(directory)
	if err != nil {
		t.Fatalf("reload NewStore() error = %v", err)
	}
	got, ok := reloaded.Get(want.DeviceID)
	if !ok || got != want {
		t.Fatalf("Get() = %#v, %v; want %#v, true", got, ok, want)
	}

	info, err := os.Stat(filepath.Join(directory, "devices.json"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("devices.json permissions = %o, want 600", permissions)
	}
}
