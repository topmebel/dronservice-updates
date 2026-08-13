package ipcamera

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
)

type DahuaDiscoveryBackend struct{}

func (DahuaDiscoveryBackend) Name() string { return "dahua" }
func (DahuaDiscoveryBackend) Discover(ctx context.Context, opts BackendOptions) ([]DiscoveredDevice, error) {
	interfaceNames := []string{opts.InterfaceName}
	if opts.InterfaceName == "" {
		var err error
		interfaceNames, err = usableIPv4Interfaces()
		if err != nil {
			return nil, err
		}
	}
	type result struct {
		devices []DahuaDevice
		err     error
	}
	results := make(chan result, len(interfaceNames))
	var wait sync.WaitGroup
	for _, interfaceName := range interfaceNames {
		wait.Add(1)
		go func(name string) {
			defer wait.Done()
			devices, err := DiscoverDahua(ctx, DahuaDiscoverOptions{InterfaceName: name, Timeout: opts.Timeout})
			results <- result{devices: devices, err: err}
		}(interfaceName)
	}
	wait.Wait()
	close(results)
	var devices []DahuaDevice
	var errs []error
	for result := range results {
		devices = append(devices, result.devices...)
		if result.err != nil {
			errs = append(errs, result.err)
		}
	}
	if len(devices) == 0 && len(errs) == len(interfaceNames) {
		return nil, errors.Join(errs...)
	}
	out := make([]DiscoveredDevice, 0, len(devices))
	for _, d := range devices {
		out = append(out, DiscoveredDevice{Vendor: "Dahua", Manufacturer: d.Manufacturer, Protocols: []string{d.Protocol}, MAC: d.MAC, IP: d.IP, SubnetMask: d.SubnetMask, Gateway: d.Gateway, Model: d.Model, SerialNumber: d.SerialNumber, FirmwareVersion: d.FirmwareVersion, DeviceType: d.DeviceClass, DeviceName: d.MachineName, HTTPPort: d.HTTPPort, ServicePort: d.ServicePort, SourceAddress: d.SourceAddress, Confidence: "high", InitializationStatus: d.InitializationStatus})
	}
	return out, nil
}

func usableIPv4Interfaces() ([]string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list discovery interfaces: %w", err)
	}
	var names []string
	for _, iface := range interfaces {
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
				names = append(names, iface.Name)
				break
			}
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no usable multicast IPv4 interface found")
	}
	return names, nil
}
