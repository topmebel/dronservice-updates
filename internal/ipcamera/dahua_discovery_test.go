package ipcamera

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

type fakeDiscoveryTransport struct {
	receive func(context.Context, time.Time) ([]byte, *net.UDPAddr, error)
	closed  bool
}

func (f *fakeDiscoveryTransport) Send([]byte) (int, error) { return 2, nil }
func (f *fakeDiscoveryTransport) Receive(ctx context.Context, deadline time.Time) ([]byte, *net.UDPAddr, error) {
	return f.receive(ctx, deadline)
}
func (f *fakeDiscoveryTransport) Close() error { f.closed = true; return nil }

func TestDiscoverWithTransportTimeout(t *testing.T) {
	fake := &fakeDiscoveryTransport{receive: func(context.Context, time.Time) ([]byte, *net.UDPAddr, error) {
		return nil, nil, context.DeadlineExceeded
	}}
	devices, err := discoverWithTransport(context.Background(), fake, buildDHIPRequest(), 10*time.Millisecond, "DHIP")
	if err != nil || len(devices) != 0 || !fake.closed {
		t.Fatalf("devices=%v error=%v closed=%v", devices, err, fake.closed)
	}
}

func TestDiscoverWithTransportCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeDiscoveryTransport{receive: func(ctx context.Context, _ time.Time) ([]byte, *net.UDPAddr, error) { return nil, nil, ctx.Err() }}
	_, err := discoverWithTransport(ctx, fake, buildDHIPRequest(), time.Second, "DHIP")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context.Canceled", err)
	}
}

func TestDiscoverWithTransportStopsWhenKnownDevicesRespond(t *testing.T) {
	calls := 0
	fake := &fakeDiscoveryTransport{receive: func(context.Context, time.Time) ([]byte, *net.UDPAddr, error) {
		calls++
		return syntheticDahuaResponse(nil), &net.UDPAddr{IP: net.ParseIP("192.168.106.108"), Port: 37810}, nil
	}}
	devices, err := discoverWithTransportUntil(context.Background(), fake, buildDHIPRequest(), time.Second, "DHIP", func(devices []DahuaDevice) bool {
		return len(devices) == 1
	})
	if err != nil || len(devices) != 1 || calls != 1 {
		t.Fatalf("devices=%d calls=%d error=%v", len(devices), calls, err)
	}
}
