package cameranetwork

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

type fakeRunner struct {
	calls       [][]string
	failCommand string
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	call := append([]string{name}, args...)
	r.calls = append(r.calls, call)
	if name == r.failCommand {
		return errors.New("failed")
	}
	return nil
}

func TestSelectAddressExcludesReservedAndAssigned(t *testing.T) {
	got, err := selectAddress(net.ParseIP("192.168.1.108"), net.CIDRMask(24, 32), net.ParseIP("192.168.1.1"), []net.IP{net.ParseIP("192.168.1.254")})
	if err != nil || got.String() != "192.168.1.253" {
		t.Fatalf("selectAddress = %v, %v", got, err)
	}
}

func TestHelperAddsValidatedTemporaryAddressWithSeparateArguments(t *testing.T) {
	dir := t.TempDir()
	request := Request{ID: "lease-1", Operation: "add", Interface: "eth0", Address: "192.168.1.254", Prefix: 24, CameraIP: "192.168.1.108", CameraMAC: "e0:2e:fe:6a:6c:27", TTLSeconds: 60}
	if err := atomicJSON(filepath.Join(dir, "camera-network.request.json"), request, 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	helper := Helper{DataDir: dir, AllowedInterfaces: map[string]bool{"eth0": true}, Runner: runner, Now: func() time.Time { return time.Unix(100, 0) }}
	if err := helper.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/sbin/ip", "address", "add", "192.168.1.254/24", "dev", "eth0", "valid_lft", "60", "preferred_lft", "60"}
	if len(runner.calls) != 2 || !reflect.DeepEqual(runner.calls[1], want) {
		t.Fatalf("calls = %#v", runner.calls)
	}
	var status Status
	data, _ := os.ReadFile(filepath.Join(dir, "camera-network.status.json"))
	_ = json.Unmarshal(data, &status)
	if status.State != "temporary_network_ready" {
		t.Fatalf("status=%+v", status)
	}
}

func TestHelperRejectsSubstitutedInterfaceBeforeCommand(t *testing.T) {
	dir := t.TempDir()
	request := Request{ID: "lease", Operation: "add", Interface: "evil0", Address: "192.168.1.254", Prefix: 24, CameraIP: "192.168.1.108", CameraMAC: "e0:2e:fe:6a:6c:27", TTLSeconds: 60}
	_ = atomicJSON(filepath.Join(dir, "camera-network.request.json"), request, 0o600)
	runner := &fakeRunner{}
	err := (Helper{DataDir: dir, AllowedInterfaces: map[string]bool{"eth0": true}, Runner: runner}).Process(context.Background())
	if err == nil || len(runner.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, runner.calls)
	}
}

func TestHelperReportsDuplicateAddressAndDoesNotAdd(t *testing.T) {
	dir := t.TempDir()
	request := Request{ID: "lease", Operation: "add", Interface: "eth0", Address: "192.168.1.254", Prefix: 24, CameraIP: "192.168.1.108", CameraMAC: "e0:2e:fe:6a:6c:27", TTLSeconds: 60}
	_ = atomicJSON(filepath.Join(dir, "camera-network.request.json"), request, 0o600)
	runner := &fakeRunner{failCommand: "/usr/sbin/arping"}
	_ = (Helper{DataDir: dir, AllowedInterfaces: map[string]bool{"eth0": true}, Runner: runner}).Process(context.Background())
	if len(runner.calls) != 1 {
		t.Fatalf("calls=%v", runner.calls)
	}
}
