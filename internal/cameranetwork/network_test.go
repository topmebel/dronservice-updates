package cameranetwork

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	calls       [][]string
	failCommand string
}

func testHelper(dir string, runner Runner) Helper {
	return Helper{DataDir: dir, AllowedInterfaces: map[string]bool{"eth0": true}, Runner: runner,
		LookPath: func(name string) (string, error) { return "/usr/bin/" + name, nil },
		InterfaceByName: func(name string) (*net.Interface, error) {
			if name != "eth0" {
				return nil, errors.New("missing")
			}
			return &net.Interface{Name: "eth0", Flags: net.FlagUp}, nil
		},
	}
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
	helper := testHelper(dir, runner)
	helper.Now = func() time.Time { return time.Unix(100, 0) }
	if err := helper.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"/usr/bin/ip", "address", "add", "192.168.1.254/24", "dev", "eth0", "valid_lft", "60", "preferred_lft", "60"}
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
	helper := testHelper(dir, runner)
	err := helper.Process(context.Background())
	if err == nil || len(runner.calls) != 0 {
		t.Fatalf("err=%v calls=%v", err, runner.calls)
	}
}

func TestHelperReportsDuplicateAddressAndDoesNotAdd(t *testing.T) {
	dir := t.TempDir()
	request := Request{ID: "lease", Operation: "add", Interface: "eth0", Address: "192.168.1.254", Prefix: 24, CameraIP: "192.168.1.108", CameraMAC: "e0:2e:fe:6a:6c:27", TTLSeconds: 60}
	_ = atomicJSON(filepath.Join(dir, "camera-network.request.json"), request, 0o600)
	runner := &fakeRunner{failCommand: "/usr/bin/arping"}
	helper := testHelper(dir, runner)
	_ = helper.Process(context.Background())
	if len(runner.calls) != 1 {
		t.Fatalf("calls=%v", runner.calls)
	}
}

func TestHelperMissingRequestIsNoOp(t *testing.T) {
	dir := t.TempDir()
	runner := &fakeRunner{}
	helper := testHelper(dir, runner)
	if err := helper.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("calls=%v", runner.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "camera-network.status.json")); !os.IsNotExist(err) {
		t.Fatal("no-op created status")
	}
}

func TestHelperReportsMissingArping(t *testing.T) {
	dir := t.TempDir()
	req := Request{ID: "lease", Operation: "add", Interface: "eth0", Address: "192.168.1.254", Prefix: 24, CameraIP: "192.168.1.108", CameraMAC: "e0:2e:fe:6a:6c:27", TTLSeconds: 60}
	_ = atomicJSON(filepath.Join(dir, "camera-network.request.json"), req, 0o600)
	helper := testHelper(dir, &fakeRunner{})
	helper.LookPath = func(name string) (string, error) {
		if name == "arping" {
			return "", errors.New("missing")
		}
		return "/usr/bin/" + name, nil
	}
	if err := helper.Process(context.Background()); err == nil {
		t.Fatal("missing arping accepted")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "camera-network.status.json"))
	if !strings.Contains(string(data), "required command arping is not installed") {
		t.Fatalf("status=%s", data)
	}
}

func TestHelperRepeatedRequestIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	req := Request{ID: "lease", Operation: "add", Interface: "eth0", Address: "192.168.1.254", Prefix: 24, CameraIP: "192.168.1.108", CameraMAC: "e0:2e:fe:6a:6c:27", TTLSeconds: 60}
	_ = atomicJSON(filepath.Join(dir, "camera-network.request.json"), req, 0o600)
	runner := &fakeRunner{}
	helper := testHelper(dir, runner)
	helper.Now = func() time.Time { return time.Unix(100, 0) }
	if err := helper.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := helper.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("repeated request executed commands: %v", runner.calls)
	}
}

func TestClientRejectsMissingAndLoopbackInterface(t *testing.T) {
	client := NewClient(t.TempDir(), time.Second)
	for _, iface := range []*net.Interface{nil, &net.Interface{Name: "lo", Flags: net.FlagUp | net.FlagLoopback}} {
		client.interfaceByName = func(string) (*net.Interface, error) {
			if iface == nil {
				return nil, errors.New("missing")
			}
			return iface, nil
		}
		_, err := client.Ensure(context.Background(), "192.168.1.108", "255.255.255.0", "192.168.1.1", "e0:2e:fe:6a:6c:27", "eth0", time.Minute)
		if err == nil {
			t.Fatal("invalid system interface accepted")
		}
	}
}

func TestRequestAndStatusFilesRemainPrivate(t *testing.T) {
	dir := t.TempDir()
	req := Request{ID: "lease", Operation: "add", Interface: "eth0", Address: "192.168.1.254", Prefix: 24, CameraIP: "192.168.1.108", CameraMAC: "e0:2e:fe:6a:6c:27", TTLSeconds: 60}
	_ = atomicJSON(filepath.Join(dir, "camera-network.request.json"), req, 0o600)
	helper := testHelper(dir, &fakeRunner{})
	if err := helper.Process(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"camera-network.request.json", "camera-network.status.json", "camera-network.leases.json", "camera-network.lock"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode=%o", name, info.Mode().Perm())
		}
	}
}

func TestHelperReleasesKernelLockAfterFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "camera-network.request.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	helper := testHelper(dir, &fakeRunner{})
	if err := helper.Process(context.Background()); err == nil {
		t.Fatal("invalid request accepted")
	}
	req := Request{ID: "lease", Operation: "add", Interface: "eth0", Address: "192.168.1.254", Prefix: 24, CameraIP: "192.168.1.108", CameraMAC: "e0:2e:fe:6a:6c:27", TTLSeconds: 60}
	if err := atomicJSON(filepath.Join(dir, "camera-network.request.json"), req, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := helper.Process(context.Background()); err != nil {
		t.Fatalf("second processing could not acquire lock: %v", err)
	}
}

func TestHelperRejectsSymlinkRequest(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(`{"id":"lease"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "camera-network.request.json")); err != nil {
		t.Fatal(err)
	}
	helper := testHelper(dir, &fakeRunner{})
	if err := helper.Process(context.Background()); err == nil {
		t.Fatal("symlink request accepted")
	}
}

func TestHelperRejectsNetworkAndBroadcastAddresses(t *testing.T) {
	for _, address := range []string{"192.168.1.0", "192.168.1.255", "127.0.0.1", "224.0.0.1", "0.0.0.0"} {
		req := Request{ID: "lease", Operation: "add", Interface: "eth0", Address: address, Prefix: 24, CameraIP: "192.168.1.108", CameraMAC: "e0:2e:fe:6a:6c:27", TTLSeconds: 60}
		helper := testHelper(t.TempDir(), &fakeRunner{})
		if err := helper.validate(req); err == nil {
			t.Errorf("address %s accepted", address)
		}
	}
}
