package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"DronService/internal/ipcamera"
)

func main() {
	interfaceName := flag.String("interface", "", "network interface used for discovery (for example eth0)")
	timeout := flag.Duration("timeout", 5*time.Second, "discovery timeout")
	legacy := flag.Bool("legacy", false, "include legacy DVRIP discovery")
	flag.Parse()
	if *interfaceName == "" {
		fmt.Fprintln(os.Stderr, "--interface is required")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("Searching on %s...\n\n", *interfaceName)
	devices, err := ipcamera.DiscoverDahua(ctx, ipcamera.DahuaDiscoverOptions{
		InterfaceName: *interfaceName,
		Timeout:       *timeout,
		IncludeLegacy: *legacy,
	})
	if err != nil {
		log.Fatal(err)
	}
	for _, device := range devices {
		fmt.Printf("Found Dahua device:\n  MAC:          %s\n  IP:           %s\n  Mask:         %s\n  Gateway:      %s\n  Model:        %s\n  Serial:       %s\n  Firmware:     %s\n  HTTP port:    %d\n  Service port: %d\n  Protocol:     %s\n\n",
			device.MAC, device.IP, net.IP(device.SubnetMask), device.Gateway, device.Model,
			device.SerialNumber, device.FirmwareVersion, device.HTTPPort, device.ServicePort, device.Protocol)
	}
}
