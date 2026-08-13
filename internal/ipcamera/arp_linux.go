//go:build linux

package ipcamera

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

type packetARPSource struct {
	fd             int
	interfaceName  string
	interfaceIndex int
	hardwareAddr   net.HardwareAddr
	localIP        net.IP
}

func newARPSource(interfaceName string) (arpSource, error) {
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_NONBLOCK, int(htons(0x0806)))
	if err != nil {
		return nil, fmt.Errorf("open AF_PACKET ARP socket: %w", err)
	}
	var selected *net.Interface
	if interfaceName != "" {
		iface, err := net.InterfaceByName(interfaceName)
		if err != nil {
			unix.Close(fd)
			return nil, err
		}
		if err := unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(0x0806), Ifindex: iface.Index}); err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("bind ARP interface: %w", err)
		}
		selected = iface
	}
	source := &packetARPSource{fd: fd, interfaceName: interfaceName}
	if selected != nil {
		source.interfaceIndex = selected.Index
		source.hardwareAddr = append(net.HardwareAddr(nil), selected.HardwareAddr...)
		addresses, _ := selected.Addrs()
		for _, address := range addresses {
			ip, _, parseErr := net.ParseCIDR(address.String())
			if parseErr == nil && ip.To4() != nil {
				source.localIP = ip.To4()
				break
			}
		}
	}
	return source, nil
}
func (s *packetARPSource) SendProbe(targetIP net.IP) error {
	target := targetIP.To4()
	if s.interfaceIndex == 0 || len(s.hardwareAddr) != 6 || s.localIP.To4() == nil || target == nil {
		return fmt.Errorf("ARP probe requires an interface with IPv4 and MAC address")
	}
	frame := make([]byte, 42)
	for i := 0; i < 6; i++ {
		frame[i] = 0xff
	}
	copy(frame[6:12], s.hardwareAddr)
	binary.BigEndian.PutUint16(frame[12:14], 0x0806)
	binary.BigEndian.PutUint16(frame[14:16], 1)
	binary.BigEndian.PutUint16(frame[16:18], 0x0800)
	frame[18], frame[19] = 6, 4
	binary.BigEndian.PutUint16(frame[20:22], 1)
	copy(frame[22:28], s.hardwareAddr)
	copy(frame[28:32], s.localIP.To4())
	copy(frame[38:42], target)
	address := &unix.SockaddrLinklayer{Protocol: htons(0x0806), Ifindex: s.interfaceIndex, Halen: 6}
	for i := 0; i < 6; i++ {
		address.Addr[i] = 0xff
	}
	if err := unix.Sendto(s.fd, frame, 0, address); err != nil {
		return fmt.Errorf("send ARP probe for %s: %w", target, err)
	}
	return nil
}
func (s *packetARPSource) ReadFrame(ctx context.Context, deadline time.Time) ([]byte, string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		if !time.Now().Before(deadline) {
			return nil, "", context.DeadlineExceeded
		}
		wait := 250 * time.Millisecond
		if remaining := time.Until(deadline); remaining < wait {
			wait = remaining
		}
		poll := []unix.PollFd{{Fd: int32(s.fd), Events: unix.POLLIN}}
		count, err := unix.Poll(poll, int(wait.Milliseconds()))
		if err != nil && !errors.Is(err, unix.EINTR) {
			return nil, "", err
		}
		if count == 0 {
			continue
		}
		buffer := make([]byte, 65535)
		n, from, err := unix.Recvfrom(s.fd, buffer, 0)
		if err != nil {
			if errors.Is(err, unix.EAGAIN) {
				continue
			}
			return nil, "", err
		}
		name := s.interfaceName
		if link, ok := from.(*unix.SockaddrLinklayer); ok && name == "" {
			if iface, e := net.InterfaceByIndex(link.Ifindex); e == nil {
				name = iface.Name
			}
		}
		return buffer[:n], name, nil
	}
}
func (s *packetARPSource) Close() error { return unix.Close(s.fd) }
func htons(value uint16) uint16         { return bits.ReverseBytes16(value) }
