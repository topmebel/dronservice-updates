//go:build !linux

package v4l2

import "fmt"

type capability struct {
	Driver       string
	Card         string
	Bus          string
	Version      uint32
	Capabilities uint32
}

func queryCapability(string) (capability, error) {
	return capability{}, fmt.Errorf("V4L2 capability detection is supported only on Linux")
}

func queryFormats(string) ([]Format, error) {
	return nil, fmt.Errorf("V4L2 format detection is supported only on Linux")
}
