package ipcamera

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"net"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
)

type unvDatagramSource interface {
	Send([]byte, *net.UDPAddr, string) error
	Read(context.Context, time.Time) ([]byte, *net.UDPAddr, string, error)
	Close() error
}
type udp3705Source struct {
	raw    *net.UDPConn
	packet *ipv4.PacketConn
}

func newUDP3705Source() (unvDatagramSource, error) {
	config := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var optionErr error
		if err := raw.Control(func(fd uintptr) {
			optionErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		}); err != nil {
			return err
		}
		return optionErr
	}}
	packetConn, err := config.ListenPacket(context.Background(), "udp4", "0.0.0.0:3705")
	if err != nil {
		return nil, fmt.Errorf("listen UNV UDP/3705: %w", err)
	}
	raw, ok := packetConn.(*net.UDPConn)
	if !ok {
		packetConn.Close()
		return nil, fmt.Errorf("UNV UDP/3705 returned unexpected socket type")
	}
	if err := raw.SetReadBuffer(1 << 20); err != nil {
		raw.Close()
		return nil, fmt.Errorf("set UNV UDP receive buffer: %w", err)
	}
	packet := ipv4.NewPacketConn(raw)
	if err := packet.SetControlMessage(ipv4.FlagInterface|ipv4.FlagDst, true); err != nil {
		raw.Close()
		return nil, fmt.Errorf("enable UNV interface control messages: %w", err)
	}
	return &udp3705Source{raw: raw, packet: packet}, nil
}
func (s *udp3705Source) Send(payload []byte, destination *net.UDPAddr, interfaceName string) error {
	var control *ipv4.ControlMessage
	if interfaceName != "" {
		iface, err := net.InterfaceByName(interfaceName)
		if err != nil {
			return fmt.Errorf("select UNV probe interface %q: %w", interfaceName, err)
		}
		control = &ipv4.ControlMessage{IfIndex: iface.Index}
	}
	if _, err := s.packet.WriteTo(payload, control, destination); err != nil {
		return fmt.Errorf("send UNV probe to %s: %w", destination, err)
	}
	return nil
}
func (s *udp3705Source) Read(ctx context.Context, deadline time.Time) ([]byte, *net.UDPAddr, string, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, "", err
		}
		if !time.Now().Before(deadline) {
			return nil, nil, "", context.DeadlineExceeded
		}
		next := time.Now().Add(250 * time.Millisecond)
		if deadline.Before(next) {
			next = deadline
		}
		_ = s.packet.SetReadDeadline(next)
		buffer := make([]byte, 65535)
		n, cm, address, err := s.packet.ReadFrom(buffer)
		if err != nil {
			if networkErr, ok := err.(net.Error); ok && networkErr.Timeout() {
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				return nil, nil, "", err
			}
			return nil, nil, "", err
		}
		source, ok := address.(*net.UDPAddr)
		if !ok {
			continue
		}
		interfaceName := ""
		if cm != nil && cm.IfIndex > 0 {
			if iface, e := net.InterfaceByIndex(cm.IfIndex); e == nil {
				interfaceName = iface.Name
			}
		}
		return buffer[:n], source, interfaceName, nil
	}
}
func (s *udp3705Source) Close() error { return s.raw.Close() }

func listenUNVUDP(ctx context.Context, source unvDatagramSource, opts BackendOptions) ([]DiscoveredDevice, error) {
	defer source.Close()
	messageID, err := newUNVMessageID()
	if err != nil {
		return nil, err
	}
	if err := source.Send(buildUNVProbe(messageID), &net.UDPAddr{IP: net.IPv4bcast, Port: 3702}, opts.InterfaceName); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(opts.Timeout)
	result := make([]DiscoveredDevice, 0)
	for {
		payload, address, interfaceName, err := source.Read(ctx, deadline)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return result, err
		}
		if opts.InterfaceName != "" && interfaceName != "" && interfaceName != opts.InterfaceName {
			continue
		}
		devices, err := ParseUNVProbeMatches(payload, address, interfaceName, messageID)
		if err != nil {
			log.Printf("UNV source=%s bytes=%d rejected=%v", address, len(payload), err)
			if opts.Verbose {
				summary := payloadDebugSummary(payload)
				fmt.Printf("UNV UDP rejected source=%s summary=%v reason=%v\n", address, summary, err)
			}
			continue
		}
		for _, device := range devices {
			log.Printf("UNV source=%s bytes=%d mac=%q name=%q", address, len(payload), device.MAC, device.DeviceName)
		}
		result = append(result, devices...)
	}
	return mergeDevices(result), nil
}

func newUNVMessageID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate UNV probe MessageID: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("urn:uuid:%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
