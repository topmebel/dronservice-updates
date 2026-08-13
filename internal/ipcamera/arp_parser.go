package ipcamera

import (
	"encoding/binary"
	"fmt"
	"net"
)

type ARPObservation struct {
	MAC        string
	SenderIP   net.IP
	TargetIP   net.IP
	Gratuitous bool
}

func parseARPFrame(frame []byte) (ARPObservation, error) {
	if len(frame) < 42 {
		return ARPObservation{}, fmt.Errorf("truncated ARP frame")
	}
	if binary.BigEndian.Uint16(frame[12:14]) != 0x0806 {
		return ARPObservation{}, fmt.Errorf("not ARP")
	}
	arp := frame[14:]
	if binary.BigEndian.Uint16(arp[0:2]) != 1 || binary.BigEndian.Uint16(arp[2:4]) != 0x0800 || arp[4] != 6 || arp[5] != 4 {
		return ARPObservation{}, fmt.Errorf("unsupported ARP format")
	}
	opcode := binary.BigEndian.Uint16(arp[6:8])
	if opcode != 1 && opcode != 2 {
		return ARPObservation{}, fmt.Errorf("unsupported ARP opcode")
	}
	ethernetMAC, arpMAC := net.HardwareAddr(frame[6:12]), net.HardwareAddr(arp[8:14])
	if !equalHardwareAddr(ethernetMAC, arpMAC) {
		return ARPObservation{}, fmt.Errorf("Ethernet and ARP sender MAC mismatch")
	}
	sender, target := net.IP(append([]byte(nil), arp[14:18]...)), net.IP(append([]byte(nil), arp[24:28]...))
	return ARPObservation{MAC: arpMAC.String(), SenderIP: sender, TargetIP: target, Gratuitous: sender.Equal(target)}, nil
}
func equalHardwareAddr(a, b net.HardwareAddr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
