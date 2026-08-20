package mavlink

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConfigValidate(t *testing.T) {
	config := DefaultConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestLoadConfigFromEnvOverridesFileDefaults(t *testing.T) {
	t.Setenv("MAVLINK_ENABLED", "true")
	t.Setenv("MAVLINK_UDP_ADDR", "127.0.0.1:14550")
	t.Setenv("MAVLINK_OUT_SYSTEM_ID", "240")
	t.Setenv("MAVLINK_TARGET_SYSTEM_ID", "1")
	t.Setenv("MAVLINK_LINK_TIMEOUT", "7s")
	t.Setenv("MAVLINK_MESSAGE_INTERVAL", "250ms")

	config := LoadConfigFromEnv(DefaultConfig())
	if !config.Enabled {
		t.Fatal("expected MAVLink to be enabled from env")
	}
	if config.UDPAddr != "127.0.0.1:14550" {
		t.Fatalf("UDPAddr = %q, want 127.0.0.1:14550", config.UDPAddr)
	}
	if config.OutSystemID != 240 {
		t.Fatalf("OutSystemID = %d, want 240", config.OutSystemID)
	}
	if config.TargetSystemID != 1 {
		t.Fatalf("TargetSystemID = %d, want 1", config.TargetSystemID)
	}
	if config.LinkTimeout != 7*time.Second {
		t.Fatalf("LinkTimeout = %s, want 7s", config.LinkTimeout)
	}
	if config.MessageInterval != 250*time.Millisecond {
		t.Fatalf("MessageInterval = %s, want 250ms", config.MessageInterval)
	}
}

func TestConfigJSONUsesReadableDurations(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"linkTimeout":"5s"`) {
		t.Fatalf("Marshal() = %s, want readable linkTimeout", data)
	}
	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if decoded.LinkTimeout != config.LinkTimeout || decoded.MessageInterval != config.MessageInterval {
		t.Fatalf("decoded durations = %s / %s", decoded.LinkTimeout, decoded.MessageInterval)
	}
}

func TestStorePersistsConfiguration(t *testing.T) {
	directory := t.TempDir()
	store, err := NewStore(directory)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	want := DefaultConfig()
	want.Enabled = true
	want.UDPAddr = "0.0.0.0:14551"
	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	reloaded, err := NewStore(directory)
	if err != nil {
		t.Fatalf("reload NewStore() error = %v", err)
	}
	got := reloaded.Config()
	if got != want {
		t.Fatalf("Config() = %#v, want %#v", got, want)
	}

	info, err := os.Stat(filepath.Join(directory, "mavlink.json"))
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("mavlink.json permissions = %o, want 600", permissions)
	}
}

func TestSnapshotMarksLinkOfflineAfterTimeout(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	service.mu.Lock()
	service.snapshot = Snapshot{
		Enabled: true,
		Config:  DefaultConfig(),
		Link: LinkStatus{
			LastSeenAt: now.Add(-6 * time.Second),
			SystemID:   1,
		},
	}
	service.mu.Unlock()

	snapshot := service.Snapshot()
	if snapshot.Link.Connected {
		t.Fatal("expected stale MAVLink link to be disconnected")
	}
}
