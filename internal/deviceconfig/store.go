package deviceconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	DeviceID    string `json:"deviceId"`
	DevicePath  string `json:"devicePath,omitempty"`
	Name        string `json:"name"`
	PixelFormat string `json:"pixelFormat"`
	Resolution  string `json:"resolution"`
	FPS         string `json:"fps"`
	Use         bool   `json:"use"`
}

func (s *Store) List() []Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	configs := make([]Config, 0, len(s.configs))
	for _, config := range s.configs {
		configs = append(configs, config)
	}
	return configs
}

type Store struct {
	mu       sync.RWMutex
	filePath string
	configs  map[string]Config
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create device configuration directory: %w", err)
	}

	store := &Store{filePath: filepath.Join(dataDir, "devices.json"), configs: make(map[string]Config)}
	data, err := os.ReadFile(store.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("read device configurations: %w", err)
	}
	if err := json.Unmarshal(data, &store.configs); err != nil {
		return nil, fmt.Errorf("decode device configurations: %w", err)
	}
	return store, nil
}

func (s *Store) Get(deviceID string) (Config, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	config, ok := s.configs[deviceID]
	return config, ok
}

func (s *Store) Save(config Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := make(map[string]Config, len(s.configs)+1)
	for id, existing := range s.configs {
		next[id] = existing
	}
	next[config.DeviceID] = config
	if err := s.write(next); err != nil {
		return err
	}
	s.configs = next
	return nil
}

func (s *Store) Delete(deviceID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.configs[deviceID]; !ok {
		return os.ErrNotExist
	}
	next := make(map[string]Config, len(s.configs)-1)
	for id, existing := range s.configs {
		if id != deviceID {
			next[id] = existing
		}
	}
	if err := s.write(next); err != nil {
		return err
	}
	s.configs = next
	return nil
}

func (s *Store) write(next map[string]Config) error {
	data, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return fmt.Errorf("encode device configurations: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.filePath), "devices-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary device configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect device configuration: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write device configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync device configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close device configuration: %w", err)
	}
	if err := os.Rename(temporaryPath, s.filePath); err != nil {
		return fmt.Errorf("replace device configuration: %w", err)
	}
	directory, err := os.Open(filepath.Dir(s.filePath))
	if err != nil {
		return fmt.Errorf("open device configuration directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("sync device configuration directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close device configuration directory: %w", err)
	}

	return nil
}
