package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"DronService/internal/ipcamera"
)

func main() {
	interfaceName := flag.String("interface", "", "network interface (empty means automatic)")
	timeout := flag.Duration("timeout", 10*time.Second, "discovery duration")
	vendor := flag.String("vendor", "all", "all, dahua, or unv")
	jsonOutput := flag.Bool("json", false, "print stable JSON output")
	verbose := flag.Bool("verbose", false, "print safe rejected-packet diagnostics")
	noARP := flag.Bool("no-arp", false, "disable passive ARP observation")
	flag.Parse()

	vendors, err := vendorFilter(*vendor)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if !*jsonOutput {
		where := *interfaceName
		if where == "" {
			where = "all usable interfaces"
		}
		fmt.Printf("Searching on %s...\n", where)
	}
	devices, err := ipcamera.DiscoverCameras(context.Background(), ipcamera.DiscoverOptions{
		InterfaceName: *interfaceName, Timeout: *timeout, Vendors: vendors,
		EnableARP: !*noARP, Verbose: *verbose,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "camera discovery:", err)
		os.Exit(1)
	}
	if *jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(struct {
			Devices []ipcamera.DiscoveredDevice `json:"devices"`
		}{Devices: devices}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	for _, device := range devices {
		fmt.Printf("\nFound %s device:\n", device.Vendor)
		fmt.Printf("  Vendor:       %s\n", device.Vendor)
		fmt.Printf("  Name:         %s\n", device.DeviceName)
		fmt.Printf("  MAC:          %s\n", device.MAC)
		fmt.Printf("  IP:           %s\n", device.IP)
		fmt.Printf("  Interface:    %s\n", device.InterfaceName)
		fmt.Printf("  Protocols:    %s\n", strings.Join(device.Protocols, ", "))
		fmt.Printf("  Confidence:   %s\n", device.Confidence)
	}
}

func vendorFilter(value string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "all":
		return []string{"dahua", "unv"}, nil
	case "dahua":
		return []string{"dahua"}, nil
	case "unv", "uniview":
		return []string{"unv"}, nil
	default:
		return nil, fmt.Errorf("unsupported vendor %q (use all, dahua, or unv)", value)
	}
}
