package mavlink

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	TransportUDP = "udp"
)

type Config struct {
	Enabled         bool          `json:"enabled"`
	Transport       string        `json:"transport"`
	UDPAddr         string        `json:"udpAddr"`
	OutSystemID     uint8         `json:"outSystemId"`
	TargetSystemID  uint8         `json:"targetSystemId"`
	LinkTimeout     time.Duration `json:"linkTimeout"`
	MessageInterval time.Duration `json:"messageInterval"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:         false,
		Transport:       TransportUDP,
		UDPAddr:         "0.0.0.0:14550",
		OutSystemID:     255,
		TargetSystemID:  0,
		LinkTimeout:     5 * time.Second,
		MessageInterval: 500 * time.Millisecond,
	}
}

func (c Config) Merge(other Config) Config {
	merged := c
	if other.Transport != "" {
		merged.Transport = other.Transport
	}
	if other.UDPAddr != "" {
		merged.UDPAddr = other.UDPAddr
	}
	if other.OutSystemID != 0 {
		merged.OutSystemID = other.OutSystemID
	}
	merged.Enabled = other.Enabled
	merged.TargetSystemID = other.TargetSystemID
	if other.LinkTimeout > 0 {
		merged.LinkTimeout = other.LinkTimeout
	}
	if other.MessageInterval > 0 {
		merged.MessageInterval = other.MessageInterval
	}
	return merged
}

func LoadConfigFromEnv(base Config) Config {
	cfg := base
	if value := strings.TrimSpace(os.Getenv("MAVLINK_ENABLED")); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err == nil {
			cfg.Enabled = enabled
		}
	}
	if value := strings.TrimSpace(os.Getenv("MAVLINK_TRANSPORT")); value != "" {
		cfg.Transport = value
	}
	if value := strings.TrimSpace(os.Getenv("MAVLINK_UDP_ADDR")); value != "" {
		cfg.UDPAddr = value
	}
	if value := strings.TrimSpace(os.Getenv("MAVLINK_OUT_SYSTEM_ID")); value != "" {
		if parsed, err := strconv.ParseUint(value, 10, 8); err == nil && parsed > 0 {
			cfg.OutSystemID = uint8(parsed)
		}
	}
	if value := strings.TrimSpace(os.Getenv("MAVLINK_TARGET_SYSTEM_ID")); value != "" {
		if parsed, err := strconv.ParseUint(value, 10, 8); err == nil {
			cfg.TargetSystemID = uint8(parsed)
		}
	}
	if value := strings.TrimSpace(os.Getenv("MAVLINK_LINK_TIMEOUT")); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			cfg.LinkTimeout = parsed
		}
	}
	if value := strings.TrimSpace(os.Getenv("MAVLINK_MESSAGE_INTERVAL")); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil && parsed > 0 {
			cfg.MessageInterval = parsed
		}
	}
	return cfg
}

func (c Config) Validate() error {
	switch c.Transport {
	case TransportUDP:
	default:
		return fmt.Errorf("unsupported MAVLink transport %q", c.Transport)
	}
	if strings.TrimSpace(c.UDPAddr) == "" {
		return fmt.Errorf("MAVLink UDP address is required")
	}
	if _, _, err := net.SplitHostPort(c.UDPAddr); err != nil {
		return fmt.Errorf("invalid MAVLink UDP address: %w", err)
	}
	if c.OutSystemID == 0 {
		return fmt.Errorf("MAVLink out system id must be between 1 and 255")
	}
	if c.LinkTimeout <= 0 {
		return fmt.Errorf("MAVLink link timeout must be positive")
	}
	if c.MessageInterval < 50*time.Millisecond {
		return fmt.Errorf("MAVLink message interval must be at least 50ms")
	}
	return nil
}

type configJSON struct {
	Enabled         bool   `json:"enabled"`
	Transport       string `json:"transport"`
	UDPAddr         string `json:"udpAddr"`
	OutSystemID     uint8  `json:"outSystemId"`
	TargetSystemID  uint8  `json:"targetSystemId"`
	LinkTimeout     string `json:"linkTimeout"`
	MessageInterval string `json:"messageInterval"`
}

func (c Config) MarshalJSON() ([]byte, error) {
	return json.Marshal(configJSON{
		Enabled:         c.Enabled,
		Transport:       c.Transport,
		UDPAddr:         c.UDPAddr,
		OutSystemID:     c.OutSystemID,
		TargetSystemID:  c.TargetSystemID,
		LinkTimeout:     c.LinkTimeout.String(),
		MessageInterval: c.MessageInterval.String(),
	})
}

func (c *Config) UnmarshalJSON(data []byte) error {
	var payload configJSON
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	c.Enabled = payload.Enabled
	if payload.Transport != "" {
		c.Transport = payload.Transport
	}
	if payload.UDPAddr != "" {
		c.UDPAddr = payload.UDPAddr
	}
	if payload.OutSystemID != 0 {
		c.OutSystemID = payload.OutSystemID
	}
	c.TargetSystemID = payload.TargetSystemID
	if payload.LinkTimeout != "" {
		parsed, err := time.ParseDuration(payload.LinkTimeout)
		if err != nil {
			return fmt.Errorf("parse linkTimeout: %w", err)
		}
		c.LinkTimeout = parsed
	}
	if payload.MessageInterval != "" {
		parsed, err := time.ParseDuration(payload.MessageInterval)
		if err != nil {
			return fmt.Errorf("parse messageInterval: %w", err)
		}
		c.MessageInterval = parsed
	}
	return nil
}
