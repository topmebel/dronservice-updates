package cameranetwork

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type Request struct {
	ID         string `json:"id"`
	Operation  string `json:"operation"`
	Interface  string `json:"interface"`
	Address    string `json:"address"`
	Prefix     int    `json:"prefix"`
	CameraIP   string `json:"cameraIP"`
	CameraMAC  string `json:"cameraMAC"`
	TTLSeconds int    `json:"ttlSeconds"`
}

type Status struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Interface string    `json:"interface,omitempty"`
	Address   string    `json:"address,omitempty"`
	Prefix    int       `json:"prefix,omitempty"`
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
	Message   string    `json:"message,omitempty"`
}

type Lease struct {
	Address, Interface string
	Prefix             int
	ExpiresAt          time.Time
}

type Client struct {
	mu                      sync.Mutex
	requestPath, statusPath string
	timeout                 time.Duration
	interfaceByName         func(string) (*net.Interface, error)
}

func NewClient(dataDir string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Client{requestPath: filepath.Join(dataDir, "camera-network.request.json"), statusPath: filepath.Join(dataDir, "camera-network.status.json"), timeout: timeout, interfaceByName: net.InterfaceByName}
}

func (c *Client) Ensure(ctx context.Context, cameraIP, mask, gateway, mac, interfaceName string, ttl time.Duration) (Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ip := net.ParseIP(cameraIP).To4()
	maskIP := net.ParseIP(mask).To4()
	if ip == nil || maskIP == nil {
		return Lease{}, errors.New("camera subnet mask is unknown or invalid")
	}
	prefix, bits := net.IPMask(maskIP).Size()
	if bits != 32 || prefix < 1 || prefix > 30 {
		return Lease{}, errors.New("camera subnet mask is unknown or invalid")
	}
	if !validInterface(interfaceName) {
		return Lease{}, errors.New("camera discovery interface is unknown or invalid")
	}
	if err := validateSystemInterface(interfaceName, c.interfaceByName); err != nil {
		return Lease{}, err
	}
	if _, err := net.ParseMAC(mac); err != nil {
		return Lease{}, errors.New("camera L2 evidence is missing")
	}
	address, err := selectAddress(ip, net.IPMask(maskIP), net.ParseIP(gateway), localIPv4())
	if err != nil {
		return Lease{}, err
	}
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	req := Request{ID: id, Operation: "add", Interface: interfaceName, Address: address.String(), Prefix: prefix, CameraIP: ip.String(), CameraMAC: mac, TTLSeconds: int(ttl.Seconds())}
	if req.TTLSeconds < 30 {
		req.TTLSeconds = 30
	}
	if req.TTLSeconds > 3600 {
		req.TTLSeconds = 3600
	}
	if err := atomicJSON(c.requestPath, req, 0o600); err != nil {
		return Lease{}, fmt.Errorf("request temporary camera network: %w", err)
	}
	deadline := time.NewTimer(c.timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return Lease{}, ctx.Err()
		case <-deadline.C:
			return Lease{}, errors.New("camera network helper timed out")
		case <-ticker.C:
			data, readErr := os.ReadFile(c.statusPath)
			if readErr != nil {
				continue
			}
			var status Status
			if json.Unmarshal(data, &status) != nil || status.ID != id {
				continue
			}
			if status.State != "temporary_network_ready" {
				if status.Message != "" {
					return Lease{}, fmt.Errorf("camera network helper: %s: %s", status.State, status.Message)
				}
				return Lease{}, fmt.Errorf("camera network helper: %s", status.State)
			}
			return Lease{Address: status.Address, Interface: status.Interface, Prefix: status.Prefix, ExpiresAt: status.ExpiresAt}, nil
		}
	}
}

func selectAddress(camera net.IP, mask net.IPMask, gateway net.IP, assigned []net.IP) (net.IP, error) {
	network := camera.Mask(mask)
	broadcast := append(net.IP(nil), network...)
	for i := range broadcast {
		broadcast[i] |= ^mask[i]
	}
	used := map[string]bool{camera.String(): true, network.String(): true, broadcast.String(): true, gateway.String(): true}
	for _, ip := range assigned {
		used[ip.String()] = true
	}
	start := binaryIPv4(broadcast) - 1
	end := binaryIPv4(network) + 1
	for value := start; value >= end && start-value < 1024; value-- {
		candidate := uintIPv4(value)
		if !used[candidate.String()] {
			return candidate, nil
		}
	}
	return nil, errors.New("no free temporary IPv4 candidate in camera subnet")
}

func binaryIPv4(ip net.IP) uint32 {
	v := ip.To4()
	return uint32(v[0])<<24 | uint32(v[1])<<16 | uint32(v[2])<<8 | uint32(v[3])
}
func uintIPv4(v uint32) net.IP { return net.IPv4(byte(v>>24), byte(v>>16), byte(v>>8), byte(v)) }
func localIPv4() []net.IP {
	addrs, _ := net.InterfaceAddrs()
	var out []net.IP
	for _, a := range addrs {
		ip, _, e := net.ParseCIDR(a.String())
		if e == nil && ip.To4() != nil {
			out = append(out, ip.To4())
		}
	}
	return out
}

type Runner interface {
	Run(context.Context, string, ...string) error
}
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

type Helper struct {
	DataDir           string
	AllowedInterfaces map[string]bool
	Runner            Runner
	Now               func() time.Time
	LookPath          func(string) (string, error)
	InterfaceByName   func(string) (*net.Interface, error)
}
type helperState struct {
	Leases map[string]Status `json:"leases"`
}

func (h Helper) Process(ctx context.Context) error {
	if h.Runner == nil {
		h.Runner = ExecRunner{}
	}
	if h.Now == nil {
		h.Now = time.Now
	}
	if h.LookPath == nil {
		h.LookPath = exec.LookPath
	}
	if h.InterfaceByName == nil {
		h.InterfaceByName = net.InterfaceByName
	}
	lockFD, err := unix.Open(filepath.Join(h.DataDir, "camera-network.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open camera network helper lock: %w", err)
	}
	lock := os.NewFile(uintptr(lockFD), "camera-network.lock")
	defer lock.Close()
	if err := lock.Chmod(0o600); err != nil {
		return fmt.Errorf("secure camera network helper lock: %w", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("lock camera network helper: %w", err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	data, err := readRequestFile(filepath.Join(h.DataDir, "camera-network.request.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) > 16<<10 {
		return errors.New("camera network request too large")
	}
	var req Request
	if err := json.Unmarshal(data, &req); err != nil {
		return h.fail(req.ID, "helper_failed", "invalid request JSON")
	}
	if err := h.validate(req); err != nil {
		return h.fail(req.ID, "helper_failed", err.Error())
	}
	state := h.readState()
	now := h.Now().UTC()
	for id, lease := range state.Leases {
		if !lease.ExpiresAt.After(now) {
			delete(state.Leases, id)
		}
	}
	if existing, ok := state.Leases[req.ID]; ok && existing.ExpiresAt.After(now) {
		if existing.Interface != req.Interface || existing.Address != req.Address || existing.Prefix != req.Prefix {
			return h.fail(req.ID, "helper_failed", "request ID conflicts with an existing lease")
		}
		return h.finish(state, existing)
	}
	if req.Operation == "remove" {
		lease, ok := state.Leases[req.ID]
		if !ok {
			return h.fail(req.ID, "helper_failed", "lease is not owned by helper")
		}
		ipCommand, err := h.command("ip")
		if err != nil {
			return h.fail(req.ID, "helper_failed", err.Error())
		}
		if err := h.Runner.Run(ctx, ipCommand, "address", "del", fmt.Sprintf("%s/%d", lease.Address, lease.Prefix), "dev", lease.Interface); err != nil {
			return h.fail(req.ID, "helper_failed", "remove temporary address failed")
		}
		delete(state.Leases, req.ID)
		return h.finish(state, Status{ID: req.ID, State: "expired"})
	}
	arpingCommand, err := h.command("arping")
	if err != nil {
		return h.fail(req.ID, "helper_failed", err.Error())
	}
	ipCommand, err := h.command("ip")
	if err != nil {
		return h.fail(req.ID, "helper_failed", err.Error())
	}
	if err := h.Runner.Run(ctx, arpingCommand, "-D", "-I", req.Interface, "-c", "2", "-w", "2", req.Address); err != nil {
		return h.fail(req.ID, "address_conflict", "ARP duplicate-address detection found a conflict")
	}
	expires := now.Add(time.Duration(req.TTLSeconds) * time.Second)
	if err := h.Runner.Run(ctx, ipCommand, "address", "add", fmt.Sprintf("%s/%d", req.Address, req.Prefix), "dev", req.Interface, "valid_lft", fmt.Sprint(req.TTLSeconds), "preferred_lft", fmt.Sprint(req.TTLSeconds)); err != nil {
		return h.fail(req.ID, "helper_failed", "add temporary address failed")
	}
	status := Status{ID: req.ID, State: "temporary_network_ready", Interface: req.Interface, Address: req.Address, Prefix: req.Prefix, ExpiresAt: expires}
	state.Leases[req.ID] = status
	return h.finish(state, status)
}

func (h Helper) validate(r Request) error {
	ip := net.ParseIP(r.Address).To4()
	camera := net.ParseIP(r.CameraIP).To4()
	mac, e := net.ParseMAC(r.CameraMAC)
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`).MatchString(r.ID) || !h.AllowedInterfaces[r.Interface] || !validInterface(r.Interface) || r.Operation != "add" && r.Operation != "remove" || ip == nil || camera == nil || !ip.IsPrivate() || ip.IsLoopback() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || r.Prefix < 1 || r.Prefix > 30 || e != nil || mac[0]&1 != 0 || r.TTLSeconds < 30 || r.TTLSeconds > 3600 {
		return errors.New("invalid camera network request")
	}
	if err := validateSystemInterface(r.Interface, h.InterfaceByName); err != nil {
		return err
	}
	n := &net.IPNet{IP: camera.Mask(net.CIDRMask(r.Prefix, 32)), Mask: net.CIDRMask(r.Prefix, 32)}
	broadcast := append(net.IP(nil), n.IP...)
	for i := range broadcast {
		broadcast[i] |= ^n.Mask[i]
	}
	if !n.Contains(ip) || ip.Equal(camera) || ip.Equal(n.IP) || ip.Equal(broadcast) {
		return errors.New("temporary address outside camera subnet")
	}
	return nil
}
func validInterface(v string) bool {
	return regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`).MatchString(v)
}
func (h Helper) readState() helperState {
	s := helperState{Leases: map[string]Status{}}
	data, _ := os.ReadFile(filepath.Join(h.DataDir, "camera-network.leases.json"))
	_ = json.Unmarshal(data, &s)
	if s.Leases == nil {
		s.Leases = map[string]Status{}
	}
	return s
}
func (h Helper) fail(id, state, message string) error {
	_ = atomicJSON(filepath.Join(h.DataDir, "camera-network.status.json"), Status{ID: id, State: state, Message: message}, 0o600)
	return errors.New(state)
}
func (h Helper) finish(state helperState, status Status) error {
	if err := atomicJSON(filepath.Join(h.DataDir, "camera-network.leases.json"), state, 0o600); err != nil {
		return err
	}
	return atomicJSON(filepath.Join(h.DataDir, "camera-network.status.json"), status, 0o600)
}

func (h Helper) command(name string) (string, error) {
	path, err := h.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("required command %s is not installed", name)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("required command %s resolved to unsafe path", name)
	}
	return path, nil
}

func validateSystemInterface(name string, lookup func(string) (*net.Interface, error)) error {
	iface, err := lookup(name)
	if err != nil {
		return errors.New("camera discovery interface does not exist")
	}
	if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
		return errors.New("camera discovery interface is loopback or down")
	}
	return nil
}

func readRequestFile(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, os.NewSyscallError("open camera network request", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("camera network request must be a private regular file")
	}
	return io.ReadAll(io.LimitReader(file, (16<<10)+1))
}
func atomicJSON(path string, value any, mode os.FileMode) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(filepath.Dir(path), ".camera-network-*.tmp")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Chmod(mode); err == nil {
		_, err = temp.Write(data)
	}
	if err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

func ParseAllowedInterfaces(value string) map[string]bool {
	out := map[string]bool{}
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		if validInterface(v) {
			out[v] = true
		}
	}
	return out
}
func SortedInterfaces(values map[string]bool) []string {
	var out []string
	for v := range values {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
