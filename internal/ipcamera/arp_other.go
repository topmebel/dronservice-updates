//go:build !linux

package ipcamera

import "fmt"

func newARPSource(string) (arpSource, error) {
	return nil, fmt.Errorf("ARP observer is supported only on Linux")
}
