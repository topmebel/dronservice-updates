//go:build linux

package v4l2

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	videoQueryCapability   = 0x80685600
	videoEnumerateFormat   = 0xc0405602
	videoEnumerateSize     = 0xc02c564a
	videoEnumerateInterval = 0xc034564b
	bufferTypeCapture      = 1
	bufferTypeCaptureMPlan = 9
)

type rawCapability struct {
	Driver       [16]byte
	Card         [32]byte
	BusInfo      [32]byte
	Version      uint32
	Capabilities uint32
	DeviceCaps   uint32
	Reserved     [3]uint32
}

type capability struct {
	Driver       string
	Card         string
	Bus          string
	Version      uint32
	Capabilities uint32
}

func queryCapability(path string) (capability, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return capability{}, fmt.Errorf("open device: %w", err)
	}
	defer file.Close()

	var raw rawCapability
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), videoQueryCapability, uintptr(unsafe.Pointer(&raw)))
	if errno != 0 {
		return capability{}, fmt.Errorf("query V4L2 capabilities: %w", errno)
	}

	capabilities := raw.Capabilities
	if raw.Capabilities&0x80000000 != 0 {
		capabilities = raw.DeviceCaps
	}

	return capability{
		Driver:       cString(raw.Driver[:]),
		Card:         cString(raw.Card[:]),
		Bus:          cString(raw.BusInfo[:]),
		Version:      raw.Version,
		Capabilities: capabilities,
	}, nil
}

type rawFormatDescription struct {
	Index       uint32
	Type        uint32
	Flags       uint32
	Description [32]byte
	PixelFormat uint32
	MBusCode    uint32
	Reserved    [3]uint32
}

type rawFrameSize struct {
	Index       uint32
	PixelFormat uint32
	Type        uint32
	Data        [6]uint32
	Reserved    [2]uint32
}

type rawFrameInterval struct {
	Index       uint32
	PixelFormat uint32
	Width       uint32
	Height      uint32
	Type        uint32
	Data        [6]uint32
	Reserved    [2]uint32
}

func queryFormats(path string) ([]Format, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open device for format query: %w", err)
	}
	defer file.Close()

	formats := make([]Format, 0)
	for _, bufferType := range []uint32{bufferTypeCapture, bufferTypeCaptureMPlan} {
		for index := uint32(0); ; index++ {
			raw := rawFormatDescription{Index: index, Type: bufferType}
			if err := ioctl(file, videoEnumerateFormat, unsafe.Pointer(&raw)); err != nil {
				if errors.Is(err, syscall.EINVAL) {
					break
				}
				return nil, fmt.Errorf("enumerate V4L2 formats: %w", err)
			}

			format := Format{
				PixelFormat: fourCC(raw.PixelFormat),
				Description: cString(raw.Description[:]),
				Modes:       enumerateModes(file, raw.PixelFormat),
			}
			formats = append(formats, format)
		}
	}
	return formats, nil
}

func enumerateModes(file *os.File, pixelFormat uint32) []Mode {
	modes := make([]Mode, 0)
	for index := uint32(0); ; index++ {
		size := rawFrameSize{Index: index, PixelFormat: pixelFormat}
		if err := ioctl(file, videoEnumerateSize, unsafe.Pointer(&size)); err != nil {
			break
		}

		if size.Type != 1 {
			modes = append(modes, Mode{
				Resolution: fmt.Sprintf("%dx%d–%dx%d", size.Data[0], size.Data[3], size.Data[1], size.Data[4]),
				FPS:        "variable",
			})
			continue
		}

		width, height := size.Data[0], size.Data[1]
		intervals := enumerateIntervals(file, pixelFormat, width, height)
		if len(intervals) == 0 {
			modes = append(modes, Mode{Resolution: fmt.Sprintf("%dx%d", width, height), FPS: "unknown"})
			continue
		}
		for _, fps := range intervals {
			modes = append(modes, Mode{Resolution: fmt.Sprintf("%dx%d", width, height), FPS: fps})
		}
	}
	return modes
}

func enumerateIntervals(file *os.File, pixelFormat, width, height uint32) []string {
	intervals := make([]string, 0)
	for index := uint32(0); ; index++ {
		interval := rawFrameInterval{
			Index:       index,
			PixelFormat: pixelFormat,
			Width:       width,
			Height:      height,
		}
		if err := ioctl(file, videoEnumerateInterval, unsafe.Pointer(&interval)); err != nil {
			break
		}
		if interval.Type != 1 {
			return []string{"variable"}
		}
		intervals = append(intervals, formatFPS(interval.Data[0], interval.Data[1]))
	}
	return intervals
}

func formatFPS(numerator, denominator uint32) string {
	if numerator == 0 {
		return "unknown"
	}
	if denominator%numerator == 0 {
		return fmt.Sprintf("%d", denominator/numerator)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", float64(denominator)/float64(numerator)), "0"), ".")
}

func fourCC(value uint32) string {
	return string([]byte{byte(value), byte(value >> 8), byte(value >> 16), byte(value >> 24)})
}

func ioctl(file *os.File, request uintptr, value unsafe.Pointer) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), request, uintptr(value))
	if errno != 0 {
		return errno
	}
	return nil
}

func cString(value []byte) string {
	if index := strings.IndexByte(string(value), 0); index >= 0 {
		value = value[:index]
	}
	return string(value)
}
