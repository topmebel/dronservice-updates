package streampreview

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"DronService/internal/stream"
)

type fakePaths struct {
	mu                     sync.Mutex
	configs                map[string]stream.Config
	sources                map[string]stream.Source
	deleted                []string
	deleteCalls            int
	deleteContextErrors    []error
	deleteContextDeadlines []bool
	listErr                error
	applyErr               error
	deleteErr              error
}

func newFakePaths() *fakePaths {
	return &fakePaths{configs: make(map[string]stream.Config), sources: make(map[string]stream.Source)}
}

func (f *fakePaths) ListInternalPreviewPaths(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	result := make([]string, 0, len(f.configs))
	for name := range f.configs {
		if stream.IsInternalPreviewPath(name) {
			result = append(result, name)
		}
	}
	return result, nil
}

func (f *fakePaths) ApplySource(_ context.Context, config stream.Config, source stream.Source, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.configs[config.Name] = config
	f.sources[config.Name] = source
	return f.applyErr
}

func (f *fakePaths) DeleteConfig(ctx context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	f.deleteContextErrors = append(f.deleteContextErrors, ctx.Err())
	_, hasDeadline := ctx.Deadline()
	f.deleteContextDeadlines = append(f.deleteContextDeadlines, hasDeadline)
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.configs, name)
	delete(f.sources, name)
	f.deleted = append(f.deleted, name)
	return nil
}

func TestManagerRejectsSessionFromDifferentCamera(t *testing.T) {
	paths := newFakePaths()
	manager := NewManager(paths, time.Minute)
	session, err := manager.Start(context.Background(), "camera-a", "rtsp://192.168.1.20/main")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(context.Background(), "camera-b", session.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Stop() error = %v, want ErrSessionNotFound", err)
	}
	if len(paths.deleted) != 0 {
		t.Fatalf("deleted = %v", paths.deleted)
	}
	if err := manager.Stop(context.Background(), "camera-a", session.ID); err != nil {
		t.Fatal(err)
	}
}

func TestManagerKeepsSessionWhenPathDeletionFails(t *testing.T) {
	paths := newFakePaths()
	manager := NewManager(paths, time.Minute)
	session, err := manager.Start(context.Background(), "camera", "rtsp://192.168.1.20/main")
	if err != nil {
		t.Fatal(err)
	}
	paths.deleteErr = errors.New("MediaMTX unavailable")
	if err := manager.Stop(context.Background(), "camera", session.ID); err == nil {
		t.Fatal("Stop() error = nil")
	}
	paths.deleteErr = nil
	if err := manager.Stop(context.Background(), "camera", session.ID); err != nil {
		t.Fatalf("retry Stop(): %v", err)
	}
}

func TestManagerCreatesAndStopsCredentialBearingPreviewPath(t *testing.T) {
	paths := newFakePaths()
	manager := NewManager(paths, time.Minute)
	session, err := manager.Start(context.Background(), "camera", "rtsp://admin:secret@192.168.1.20/main")
	if err != nil {
		t.Fatal(err)
	}
	if !stream.IsInternalPreviewPath(session.Path) || session.ID == "" {
		t.Fatalf("session = %+v", session)
	}
	if source := paths.sources[session.Path]; source.Type != "ip" || source.Input != "rtsp://admin:secret@192.168.1.20/main" {
		t.Fatalf("source = %+v", source)
	}
	if err := manager.Stop(context.Background(), "camera", session.ID); err != nil {
		t.Fatal(err)
	}
	if len(paths.deleted) != 1 || paths.deleted[0] != session.Path {
		t.Fatalf("deleted = %v", paths.deleted)
	}
}

func TestManagerCleansPotentialPathAfterCanceledApply(t *testing.T) {
	paths := newFakePaths()
	paths.applyErr = context.Canceled
	manager := NewManager(paths, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := manager.Start(ctx, "camera", "rtsp://192.168.1.20/main")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start() error = %v, want context.Canceled", err)
	}
	paths.mu.Lock()
	defer paths.mu.Unlock()
	if len(paths.configs) != 0 || len(paths.deleted) != 1 || paths.deleteCalls != 1 {
		t.Fatalf("configs=%v deleted=%v deleteCalls=%d", paths.configs, paths.deleted, paths.deleteCalls)
	}
	if paths.deleteContextErrors[0] != nil {
		t.Fatalf("cleanup context error = %v, want nil", paths.deleteContextErrors[0])
	}
	if !paths.deleteContextDeadlines[0] {
		t.Fatal("cleanup context has no deadline")
	}
	if len(manager.sessions) != 0 {
		t.Fatalf("sessions = %v, want none", manager.sessions)
	}
}

func TestManagerReportsApplyAndCleanupErrors(t *testing.T) {
	applyErr := errors.New("apply response lost")
	cleanupErr := errors.New("MediaMTX unavailable")
	paths := newFakePaths()
	paths.applyErr = applyErr
	paths.deleteErr = cleanupErr
	manager := NewManager(paths, time.Minute)

	_, err := manager.Start(context.Background(), "camera", "rtsp://192.168.1.20/main")
	if !errors.Is(err, applyErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Start() error = %v, want both apply and cleanup causes", err)
	}
	if !strings.Contains(err.Error(), "clean up potential preview path") {
		t.Fatalf("Start() error = %v", err)
	}
}

func TestManagerStartSourceKeepsValidatedAnalogSettings(t *testing.T) {
	paths := newFakePaths()
	manager := NewManager(paths, time.Minute)
	source := stream.Source{
		Type:        "analog",
		DevicePath:  "/dev/video2",
		PixelFormat: "MJPG",
		Resolution:  "720x576",
		FPS:         "25",
	}
	session, err := manager.StartSource(context.Background(), "analog:usb-camera", source)
	if err != nil {
		t.Fatal(err)
	}
	if got := paths.sources[session.Path]; got != source {
		t.Fatalf("source = %+v, want %+v", got, source)
	}
	if err := manager.Stop(context.Background(), "analog:usb-camera", session.ID); err != nil {
		t.Fatal(err)
	}
}

func TestManagerReplacesActiveSessionForSameOwner(t *testing.T) {
	paths := newFakePaths()
	manager := NewManager(paths, time.Minute)
	source := stream.Source{Type: "analog", DevicePath: "/dev/video2", PixelFormat: "MJPG", Resolution: "720x576", FPS: "25"}
	first, err := manager.StartSource(context.Background(), "analog:camera-a", source)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.StartSource(context.Background(), "analog:camera-a", source)
	if err != nil {
		t.Fatalf("replacement StartSource(): %v", err)
	}
	if first.ID == second.ID || len(paths.deleted) != 1 || paths.deleted[0] != first.Path {
		t.Fatalf("first=%+v second=%+v deleted=%v", first, second, paths.deleted)
	}
	if len(paths.configs) != 1 || paths.configs[second.Path].Name != second.Path {
		t.Fatalf("replacement paths = %+v", paths.configs)
	}
	if _, err := manager.StartSource(context.Background(), "analog:camera-b", source); err != nil {
		t.Fatalf("different owner StartSource(): %v", err)
	}
	if len(paths.configs) != 2 {
		t.Fatalf("different owner paths = %d, want 2", len(paths.configs))
	}
}

func TestManagerKeepsActiveSessionWhenReplacementDeletionFails(t *testing.T) {
	paths := newFakePaths()
	manager := NewManager(paths, time.Minute)
	first, err := manager.Start(context.Background(), "camera", "rtsp://camera/main")
	if err != nil {
		t.Fatal(err)
	}
	paths.deleteErr = errors.New("MediaMTX unavailable")
	if _, err := manager.Start(context.Background(), "camera", "rtsp://camera/sub"); err == nil || !strings.Contains(err.Error(), "replace existing preview path") {
		t.Fatalf("replacement error = %v", err)
	}
	if len(paths.configs) != 1 || paths.configs[first.Path].Name != first.Path {
		t.Fatalf("active path was lost: %+v", paths.configs)
	}
}

func TestManagerExpiresPreviewPath(t *testing.T) {
	paths := newFakePaths()
	manager := NewManager(paths, 10*time.Millisecond)
	session, err := manager.Start(context.Background(), "camera", "rtsp://192.168.1.20/main")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		paths.mu.Lock()
		deleted := len(paths.deleted) > 0
		paths.mu.Unlock()
		if deleted {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("preview path %q was not deleted", session.Path)
}

func TestManagerRetriesFailedExpiration(t *testing.T) {
	paths := newFakePaths()
	paths.deleteErr = errors.New("MediaMTX unavailable")
	manager := NewManager(paths, 10*time.Millisecond)
	manager.expiryRetryDelay = 5 * time.Millisecond
	session, err := manager.Start(context.Background(), "camera", "rtsp://192.168.1.20/main")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		paths.mu.Lock()
		attempted := paths.deleteCalls > 0
		paths.mu.Unlock()
		if attempted {
			break
		}
		time.Sleep(time.Millisecond)
	}
	paths.mu.Lock()
	if paths.deleteCalls == 0 {
		paths.mu.Unlock()
		t.Fatal("expiration deletion was not attempted")
	}
	paths.deleteErr = nil
	paths.mu.Unlock()
	for time.Now().Before(deadline) {
		paths.mu.Lock()
		_, exists := paths.configs[session.Path]
		paths.mu.Unlock()
		if !exists {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("preview path %q was not deleted after retry", session.Path)
}

func TestManagerCleanupRemovesOnlyReservedPaths(t *testing.T) {
	paths := newFakePaths()
	stalePath := stream.InternalPreviewPathPrefix + "0123456789abcdef01234567"
	paths.configs[stalePath] = stream.Config{Name: stalePath}
	paths.configs[stream.InternalPreviewPathPrefix+"operator"] = stream.Config{Name: stream.InternalPreviewPathPrefix + "operator"}
	paths.configs["camera1"] = stream.Config{Name: "camera1"}
	manager := NewManager(paths, time.Minute)
	if err := manager.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths.deleted) != 1 || paths.deleted[0] != stalePath {
		t.Fatalf("deleted = %v", paths.deleted)
	}
}

func TestManagerCleanupReturnsListError(t *testing.T) {
	paths := newFakePaths()
	paths.listErr = errors.New("MediaMTX unavailable")
	manager := NewManager(paths, time.Minute)
	if err := manager.Cleanup(context.Background()); err == nil || !strings.Contains(err.Error(), "list stale preview paths") {
		t.Fatalf("Cleanup() error = %v", err)
	}
}

func TestManagerCloseDeletesSessionsAndRejectsNewOnes(t *testing.T) {
	paths := newFakePaths()
	manager := NewManager(paths, time.Minute)
	for _, cameraID := range []string{"camera-a", "camera-b"} {
		if _, err := manager.Start(context.Background(), cameraID, "rtsp://192.168.1.20/main"); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(paths.deleted) != 2 || len(paths.configs) != 0 {
		t.Fatalf("deleted = %v, configs = %v", paths.deleted, paths.configs)
	}
	if _, err := manager.Start(context.Background(), "camera-c", "rtsp://192.168.1.20/main"); err == nil {
		t.Fatal("Start() after Close() error = nil")
	}
}
