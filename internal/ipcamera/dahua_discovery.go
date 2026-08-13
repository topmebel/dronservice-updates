package ipcamera

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
)

type DahuaDiscoverOptions struct {
	InterfaceName string
	Timeout       time.Duration
	IncludeLegacy bool
}

type discoveryTransport interface {
	Send([]byte) (int, error)
	Receive(context.Context, time.Time) ([]byte, *net.UDPAddr, error)
	Close() error
}

func DiscoverDahua(ctx context.Context, opts DahuaDiscoverOptions) ([]DahuaDevice, error) {
	return discoverDahua(ctx, opts, nil)
}

func discoverKnownDahua(ctx context.Context, opts DahuaDiscoverOptions, expected map[string]struct{}) ([]DahuaDevice, error) {
	return discoverDahua(ctx, opts, func(devices []DahuaDevice) bool {
		if len(expected) == 0 {
			return true
		}
		found := make(map[string]struct{}, len(devices))
		for _, device := range devices {
			found[deviceKey(device)] = struct{}{}
		}
		for key := range expected {
			if _, ok := found[key]; !ok {
				return false
			}
		}
		return true
	})
}

func discoverDahua(ctx context.Context, opts DahuaDiscoverOptions, stopWhen func([]DahuaDevice) bool) ([]DahuaDevice, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Second
	}
	iface, localIP, err := selectInterface(opts.InterfaceName)
	if err != nil {
		return nil, err
	}
	log.Printf("Dahua discovery interface=%s IPv4=%s", iface.Name, localIP)
	transport, err := newDHIPTransport(ctx, iface, localIP)
	if err != nil {
		return nil, err
	}
	devices, err := discoverWithTransportUntil(ctx, transport, buildDHIPRequest(), opts.Timeout, "DHIP", stopWhen)
	if err != nil {
		return nil, err
	}
	if opts.IncludeLegacy {
		legacy, err := newLegacyTransport(ctx, localIP)
		if err != nil {
			log.Printf("Dahua legacy discovery unavailable: %v", err)
		} else {
			legacyDevices, legacyErr := discoverWithTransport(ctx, legacy, legacyRequest[:], opts.Timeout, "DVRIP")
			if legacyErr != nil && !errors.Is(legacyErr, context.Canceled) {
				log.Printf("Dahua legacy discovery: %v", legacyErr)
			}
			devices = append(devices, legacyDevices...)
		}
	}
	return deduplicateDevices(devices), ctx.Err()
}

func discoverWithTransport(ctx context.Context, transport discoveryTransport, request []byte, timeout time.Duration, protocol string) ([]DahuaDevice, error) {
	return discoverWithTransportUntil(ctx, transport, request, timeout, protocol, nil)
}

func discoverWithTransportUntil(ctx context.Context, transport discoveryTransport, request []byte, timeout time.Duration, protocol string, stopWhen func([]DahuaDevice) bool) ([]DahuaDevice, error) {
	defer transport.Close()
	sent, err := transport.Send(request)
	if err != nil {
		return nil, fmt.Errorf("send %s discovery: %w", protocol, err)
	}
	log.Printf("Dahua discovery protocol=%s requests=%d", protocol, sent)
	deadline := time.Now().Add(timeout)
	devices := make([]DahuaDevice, 0)
	for {
		packet, source, err := transport.Receive(ctx, deadline)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return nil, err
		}
		log.Printf("Dahua discovery response source=%s", source)
		device, err := parseDahuaPacket(packet, source)
		if err != nil {
			log.Printf("Dahua discovery rejected source=%s reason=%v", source, err)
			continue
		}
		device.Protocol = protocol
		log.Printf("Dahua device MAC=%s IP=%s model=%s", device.MAC, device.IP, device.Model)
		devices = append(devices, device)
		if stopWhen != nil {
			current := deduplicateDevices(devices)
			if stopWhen(current) {
				return current, nil
			}
		}
	}
	return deduplicateDevices(devices), nil
}

func deduplicateDevices(devices []DahuaDevice) []DahuaDevice {
	seen := make(map[string]DahuaDevice)
	for _, device := range devices {
		seen[deviceKey(device)] = device
	}
	result := make([]DahuaDevice, 0, len(seen))
	for _, device := range seen {
		result = append(result, device)
	}
	return result
}

func selectInterface(name string) (*net.Interface, net.IP, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, nil, fmt.Errorf("list network interfaces: %w", err)
	}
	for index := range interfaces {
		iface := &interfaces[index]
		if name != "" && iface.Name != name {
			continue
		}
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil {
				return iface, ip.To4(), nil
			}
		}
	}
	if name != "" {
		return nil, nil, fmt.Errorf("interface %q has no usable multicast IPv4 address", name)
	}
	return nil, nil, fmt.Errorf("no usable multicast IPv4 interface found")
}

type udpTransport struct {
	packet       *ipv4.PacketConn
	destinations []*net.UDPAddr
}

func newDHIPTransport(ctx context.Context, iface *net.Interface, localIP net.IP) (*udpTransport, error) {
	packet, err := listenPacket(ctx, net.IPv4zero, 37810, false)
	if err != nil {
		return nil, fmt.Errorf("listen DHIP UDP/37810: %w", err)
	}
	p := ipv4.NewPacketConn(packet)
	if err := p.SetMulticastInterface(iface); err != nil {
		packet.Close()
		return nil, err
	}
	if err := p.SetMulticastTTL(1); err != nil {
		packet.Close()
		return nil, err
	}
	groups := []net.IP{net.ParseIP("239.255.255.251"), net.ParseIP("239.255.255.231")}
	for _, group := range groups {
		if err := p.JoinGroup(iface, &net.UDPAddr{IP: group}); err != nil {
			packet.Close()
			return nil, fmt.Errorf("join multicast group %s: %w", group, err)
		}
	}
	destinations := []*net.UDPAddr{{IP: groups[0], Port: 37810}, {IP: groups[1], Port: 37810}}
	for _, destination := range destinations {
		log.Printf("Dahua discovery destination=%s", destination)
	}
	return &udpTransport{packet: p, destinations: destinations}, nil
}

func newLegacyTransport(ctx context.Context, localIP net.IP) (*udpTransport, error) {
	packet, err := listenPacket(ctx, localIP, 5051, true)
	if err != nil {
		return nil, err
	}
	return &udpTransport{packet: ipv4.NewPacketConn(packet), destinations: []*net.UDPAddr{{IP: net.IPv4bcast, Port: 5050}}}, nil
}

func listenPacket(ctx context.Context, localIP net.IP, port int, broadcast bool) (net.PacketConn, error) {
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var controlErr error
		err := raw.Control(func(fd uintptr) {
			controlErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			if controlErr == nil && broadcast {
				controlErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
			}
		})
		if err != nil {
			return err
		}
		return controlErr
	}}
	return config.ListenPacket(ctx, "udp4", (&net.UDPAddr{IP: localIP, Port: port}).String())
}

func (t *udpTransport) Send(payload []byte) (int, error) {
	for _, destination := range t.destinations {
		if _, err := t.packet.WriteTo(payload, nil, destination); err != nil {
			return 0, err
		}
	}
	return len(t.destinations), nil
}
func (t *udpTransport) Receive(ctx context.Context, deadline time.Time) ([]byte, *net.UDPAddr, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		next := time.Now().Add(250 * time.Millisecond)
		if deadline.Before(next) {
			next = deadline
		}
		if !time.Now().Before(deadline) {
			return nil, nil, context.DeadlineExceeded
		}
		_ = t.packet.SetReadDeadline(next)
		buffer := make([]byte, 64<<10)
		count, _, address, err := t.packet.ReadFrom(buffer)
		if err != nil {
			if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
				continue
			}
			return nil, nil, err
		}
		source, ok := address.(*net.UDPAddr)
		if !ok {
			continue
		}
		return buffer[:count], source, nil
	}
}
func (t *udpTransport) Close() error { return t.packet.Close() }
