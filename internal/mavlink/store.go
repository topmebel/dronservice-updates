package mavlink

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu       sync.RWMutex
	filePath string
	config   Config
	loaded   bool
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create MAVLink data directory: %w", err)
	}
	store := &Store{filePath: filepath.Join(dataDir, "mavlink.json")}
	if err := store.loadFromDisk(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Config() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Store) Save(config Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.write(config); err != nil {
		return err
	}
	s.config = config
	s.loaded = true
	return nil
}

func (s *Store) loadFromDisk() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read MAVLink configuration: %w", err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("decode MAVLink configuration: %w", err)
	}
	s.config = config
	s.loaded = true
	return nil
}

func (s *Store) write(config Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode MAVLink configuration: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.filePath), "mavlink-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary MAVLink configuration: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect MAVLink configuration: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write MAVLink configuration: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync MAVLink configuration: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close MAVLink configuration: %w", err)
	}
	if err := os.Rename(temporaryPath, s.filePath); err != nil {
		return fmt.Errorf("replace MAVLink configuration: %w", err)
	}
	return nil
}
