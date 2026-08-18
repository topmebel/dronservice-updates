package streampreview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"DronService/internal/stream"
)

var ErrSessionNotFound = errors.New("stream preview session not found")

const failedStartCleanupTimeout = 10 * time.Second

type PathService interface {
	ListInternalPreviewPaths(context.Context) ([]string, error)
	ApplySource(context.Context, stream.Config, stream.Source, string) error
	DeleteConfig(context.Context, string) error
}

type Session struct {
	ID        string
	Path      string
	CameraID  string
	ExpiresAt time.Time
}

type Manager struct {
	mu               sync.Mutex
	paths            PathService
	ttl              time.Duration
	expiryRetryDelay time.Duration
	sessions         map[string]*managedSession
	closed           bool
}

type managedSession struct {
	Session
	timer *time.Timer
}

func NewManager(paths PathService, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &Manager{paths: paths, ttl: ttl, expiryRetryDelay: 30 * time.Second, sessions: make(map[string]*managedSession)}
}

func (m *Manager) Start(ctx context.Context, cameraID, sourceURL string) (Session, error) {
	return m.StartSource(ctx, cameraID, stream.Source{Type: "ip", Input: sourceURL})
}

// StartSource creates a temporary MediaMTX path for an already validated
// DronService stream source. The caller owns source validation and must not
// expose credential-bearing source fields to remote clients.
func (m *Manager) StartSource(ctx context.Context, cameraID string, source stream.Source) (Session, error) {
	tokenBytes := make([]byte, 12)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Session{}, fmt.Errorf("generate preview session ID: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)
	session := Session{
		ID:        token,
		Path:      stream.InternalPreviewPathPrefix + token,
		CameraID:  cameraID,
		ExpiresAt: time.Now().Add(m.ttl),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Session{}, errors.New("stream preview manager is closed")
	}
	for activeID, active := range m.sessions {
		if active.CameraID == cameraID {
			if err := m.paths.DeleteConfig(ctx, active.Path); err != nil {
				return Session{}, fmt.Errorf("replace existing preview path: %w", err)
			}
			delete(m.sessions, activeID)
			active.timer.Stop()
		}
	}
	if err := m.paths.ApplySource(ctx, stream.Config{Name: session.Path}, source, ""); err != nil {
		// MediaMTX may have applied the configuration even when the caller's
		// request was canceled before the response arrived. Use a fresh bounded
		// context so cancellation cannot strand a credential-bearing path.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), failedStartCleanupTimeout)
		cleanupErr := m.paths.DeleteConfig(cleanupCtx, session.Path)
		cancel()
		if cleanupErr != nil {
			return Session{}, fmt.Errorf("create preview path: %w", errors.Join(err, fmt.Errorf("clean up potential preview path: %w", cleanupErr)))
		}
		return Session{}, fmt.Errorf("create preview path: %w", err)
	}
	managed := &managedSession{Session: session}
	managed.timer = time.AfterFunc(m.ttl, func() { m.expire(token) })
	m.sessions[token] = managed
	return session, nil
}

func (m *Manager) Stop(ctx context.Context, cameraID, sessionID string) error {
	m.mu.Lock()
	managed := m.sessions[sessionID]
	if managed == nil || managed.CameraID != cameraID {
		m.mu.Unlock()
		return ErrSessionNotFound
	}
	if err := m.paths.DeleteConfig(ctx, managed.Path); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("delete preview path: %w", err)
	}
	delete(m.sessions, sessionID)
	managed.timer.Stop()
	m.mu.Unlock()
	return nil
}

func (m *Manager) Cleanup(ctx context.Context) error {
	paths, err := m.paths.ListInternalPreviewPaths(ctx)
	if err != nil {
		return fmt.Errorf("list stale preview paths: %w", err)
	}
	var errs []error
	for _, path := range paths {
		if err := m.paths.DeleteConfig(ctx, path); err != nil {
			errs = append(errs, fmt.Errorf("delete stale preview path %q: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*managedSession, 0, len(m.sessions))
	for id, managed := range m.sessions {
		delete(m.sessions, id)
		managed.timer.Stop()
		sessions = append(sessions, managed)
	}
	m.mu.Unlock()
	var errs []error
	for _, managed := range sessions {
		if err := m.paths.DeleteConfig(ctx, managed.Path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) expire(sessionID string) {
	m.mu.Lock()
	managed := m.sessions[sessionID]
	if managed == nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	err := m.paths.DeleteConfig(ctx, managed.Path)
	cancel()
	if err != nil {
		managed.timer = time.AfterFunc(m.expiryRetryDelay, func() { m.expire(sessionID) })
		m.mu.Unlock()
		log.Printf("delete expired stream preview path: %v", err)
		return
	}
	delete(m.sessions, sessionID)
	m.mu.Unlock()
}
