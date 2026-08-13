package ipcamera

import (
	"context"
	"errors"
	"log"
	"net"
	"time"
)

type UNVDiscoveryBackend struct {
	UDPFactory func() (unvDatagramSource, error)
	ARPFactory func(string) (arpSource, error)
}

func discoverKnownUNVARP(ctx context.Context, interfaceName string, timeout time.Duration, expected map[string]net.IP) ([]DiscoveredDevice, error) {
	if len(expected) == 0 {
		return nil, nil
	}
	source, err := newARPSource(interfaceName)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	for _, ip := range expected {
		if err := source.SendProbe(ip); err != nil {
			return nil, err
		}
	}
	deadline := time.Now().Add(timeout)
	devices := make([]DiscoveredDevice, 0, len(expected))
	seen := make(map[string]bool, len(expected))
	for len(seen) < len(expected) {
		frame, receivedInterface, err := source.ReadFrame(ctx, deadline)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return devices, err
		}
		observation, err := parseARPFrame(frame)
		if err != nil {
			continue
		}
		id := "mac:" + normalizeMAC(observation.MAC)
		expectedIP, ok := expected[id]
		if !ok || !observation.SenderIP.Equal(expectedIP) || seen[id] {
			continue
		}
		seen[id] = true
		devices = append(devices, DiscoveredDevice{Vendor: "UNV", Protocols: []string{"UNV-ARP"}, MAC: observation.MAC, IP: observation.SenderIP, InterfaceName: receivedInterface, Confidence: "high"})
	}
	return devices, nil
}

func (UNVDiscoveryBackend) Name() string { return "unv" }
func (b UNVDiscoveryBackend) Discover(ctx context.Context, opts BackendOptions) ([]DiscoveredDevice, error) {
	udpFactory := b.UDPFactory
	if udpFactory == nil {
		udpFactory = newUDP3705Source
	}
	arpFactory := b.ARPFactory
	if arpFactory == nil {
		arpFactory = newARPSource
	}
	type result struct {
		devices []DiscoveredDevice
		err     error
	}
	channels := make(chan result, 2)
	workers := 1
	udp, err := udpFactory()
	if err != nil {
		channels <- result{err: err}
	} else {
		go func() { d, e := listenUNVUDP(ctx, udp, opts); channels <- result{d, e} }()
	}
	if opts.EnableARP {
		workers++
		arp, err := arpFactory(opts.InterfaceName)
		if err != nil {
			log.Printf("UNV ARP observer warning: %v", err)
			channels <- result{err: err}
		} else {
			go func() { d, e := observeUNVARP(ctx, arp, opts); channels <- result{d, e} }()
		}
	}
	all := make([]DiscoveredDevice, 0)
	errs := make([]error, 0)
	for i := 0; i < workers; i++ {
		item := <-channels
		all = append(all, item.devices...)
		if item.err != nil {
			errs = append(errs, item.err)
		}
	}
	if len(all) == 0 && len(errs) == workers {
		return nil, errors.Join(errs...)
	}
	return mergeDevices(all), nil
}

func observeUNVARP(ctx context.Context, source arpSource, opts BackendOptions) ([]DiscoveredDevice, error) {
	defer source.Close()
	deadline := time.Now().Add(opts.Timeout)
	devices := make([]DiscoveredDevice, 0)
	for {
		frame, interfaceName, err := source.ReadFrame(ctx, deadline)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			return devices, err
		}
		if opts.InterfaceName != "" && interfaceName != "" && interfaceName != opts.InterfaceName {
			continue
		}
		observation, err := parseARPFrame(frame)
		if err != nil {
			continue
		}
		if observation.SenderIP.IsUnspecified() {
			continue
		}
		if !isUNVOUI(observation.MAC) {
			continue
		}
		protocol := "UNV-ARP"
		if observation.Gratuitous {
			protocol = "UNV-Gratuitous-ARP"
		}
		devices = append(devices, DiscoveredDevice{Vendor: "UNV", Protocols: []string{protocol}, MAC: normalizeMAC(observation.MAC), IP: observation.SenderIP, InterfaceName: interfaceName, Confidence: "high", RawFields: map[string]any{"arpTargetIP": observation.TargetIP.String(), "gratuitous": observation.Gratuitous}})
	}
	return mergeDevices(devices), nil
}
