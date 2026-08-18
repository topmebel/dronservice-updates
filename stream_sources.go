package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"

	"DronService/internal/deviceconfig"
	"DronService/internal/ipcamera"
	"DronService/internal/stream"
	"DronService/internal/v4l2"
)

var (
	errAnalogDeviceNotFound      = errors.New("analog camera is not connected")
	errAnalogDeviceNotConfigured = errors.New("analog camera is not configured")
	errAnalogDeviceModeInvalid   = errors.New("analog camera mode is not available")
	errAnalogPreviewUnavailable  = errors.New("analog preview runtime is not available")
	analogDevicePathPattern      = regexp.MustCompile(`^/dev/video[0-9]+$`)
	analogResolutionPattern      = regexp.MustCompile(`^[0-9]{2,5}x[0-9]{2,5}$`)
	analogFPSPattern             = regexp.MustCompile(`^[0-9]{1,3}(?:\.[0-9]+)?$`)
)

type videoDeviceScanner interface {
	Scan(context.Context) ([]v4l2.Device, error)
}

type streamSourceCatalog struct {
	scanner    videoDeviceScanner
	devices    *deviceconfig.Store
	ipCameras  *ipcamera.Service
	ffmpegPath string
}

func (c streamSourceCatalog) List(ctx context.Context) ([]stream.Source, error) {
	devices, err := c.scanner.Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan analog camera sources: %w", err)
	}
	deviceByID := make(map[string]v4l2.Device, len(devices))
	deviceByPath := make(map[string]v4l2.Device, len(devices))
	for _, device := range devices {
		deviceByID[device.ID] = device
		deviceByPath[device.Path] = device
	}
	sources := make([]stream.Source, 0)
	for _, config := range c.devices.List() {
		if !config.Use {
			continue
		}
		device, ok := deviceByPath[config.DevicePath]
		if !ok {
			device, ok = deviceByID[config.DeviceID]
		}
		if !ok {
			continue
		}
		sources = append(sources, stream.Source{
			ID: "analog:" + config.DeviceID, Type: "analog", Name: config.Name,
			DevicePath: device.Path, PixelFormat: config.PixelFormat, Resolution: config.Resolution, FPS: config.FPS,
		})
	}
	for _, camera := range c.ipCameras.StreamSources() {
		sources = append(sources, stream.Source{
			ID:          "ip:" + camera.ID,
			Type:        "ip",
			Name:        camera.Name,
			Detail:      camera.Detail,
			Input:       camera.URL,
			Resolution:  camera.Metadata.Resolution,
			FPS:         camera.Metadata.FPS,
			BitrateKbps: camera.Metadata.BitrateKbps,
		})
	}
	sort.Slice(sources, func(i, j int) bool {
		if sources[i].Type == sources[j].Type {
			return sources[i].Name < sources[j].Name
		}
		return sources[i].Type < sources[j].Type
	})
	return sources, nil
}

func (c streamSourceCatalog) Resolve(ctx context.Context, id string) (stream.Source, error) {
	sources, err := c.List(ctx)
	if err != nil {
		return stream.Source{}, err
	}
	for _, source := range sources {
		if source.ID == id {
			return source, nil
		}
	}
	return stream.Source{}, fmt.Errorf("selected camera is not marked for use")
}

// ResolveAnalogPreview resolves a currently connected device from its stable
// V4L2 bus ID and revalidates the persisted capture mode against the latest
// capability scan. The Use flag is intentionally ignored: preview does not
// create a permanent public stream configuration.
func (c streamSourceCatalog) ResolveAnalogPreview(ctx context.Context, deviceID string) (stream.Source, error) {
	ffmpegPath := c.ffmpegPath
	if ffmpegPath == "" {
		ffmpegPath = "/usr/bin/ffmpeg"
	}
	info, err := os.Stat(ffmpegPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return stream.Source{}, errAnalogPreviewUnavailable
	}

	devices, err := c.scanner.Scan(ctx)
	if err != nil {
		return stream.Source{}, fmt.Errorf("scan analog camera for preview: %w", err)
	}

	var device v4l2.Device
	found := false
	for _, candidate := range devices {
		if candidate.ID == deviceID {
			device = candidate
			found = true
			break
		}
	}
	if !found {
		return stream.Source{}, errAnalogDeviceNotFound
	}

	config, ok := c.devices.Get(deviceID)
	if !ok || strings.TrimSpace(config.Name) == "" || strings.TrimSpace(config.PixelFormat) == "" || strings.TrimSpace(config.Resolution) == "" || strings.TrimSpace(config.FPS) == "" {
		return stream.Source{}, errAnalogDeviceNotConfigured
	}

	validated := config
	validated.DevicePath = device.Path
	if !validDeviceConfig(devices, validated) {
		return stream.Source{}, errAnalogDeviceModeInvalid
	}
	switch strings.ToUpper(config.PixelFormat) {
	case "MJPG", "MJPEG", "YUYV", "YUY2":
	default:
		return stream.Source{}, errAnalogDeviceModeInvalid
	}

	return stream.Source{
		ID:          "analog:" + config.DeviceID,
		Type:        "analog",
		Name:        config.Name,
		DevicePath:  device.Path,
		PixelFormat: config.PixelFormat,
		Resolution:  config.Resolution,
		FPS:         config.FPS,
	}, nil
}

func decorateStreamConfigs(configs []stream.Config, sources []stream.Source, publicRTSPBase string) []stream.Config {
	for index := range configs {
		configs[index].RTSPPath = strings.TrimRight(publicRTSPBase, "/") + "/" + configs[index].Name
		analogSettings, hasAnalogSettings := parseAnalogRunOnDemand(configs[index].RunOnDemand)
		for _, source := range sources {
			matches := source.Type == "analog" && hasAnalogSettings && analogSettings.DevicePath == source.DevicePath
			if source.Type == "ip" {
				matches = rtspWithoutCredentials(source.Input) == configs[index].Source
			}
			if matches {
				configs[index].SourceID = source.ID
				configs[index].SourceType = source.Type
				configs[index].SourceName = source.Name
				configs[index].SourceDetail = source.Detail
				configs[index].Resolution = source.Resolution
				configs[index].FPS = source.FPS
				configs[index].BitrateKbps = source.BitrateKbps
				if source.Type == "analog" {
					configs[index].BitrateKbps = 0
					if analogSettings.Resolution != "" {
						configs[index].Resolution = analogSettings.Resolution
					}
					if analogSettings.FPS != "" {
						configs[index].FPS = analogSettings.FPS
					}
				}
				break
			}
		}
	}
	return configs
}

type analogRunOnDemandSettings struct {
	DevicePath  string
	PixelFormat string
	Resolution  string
	FPS         string
}

// parseAnalogRunOnDemand reads only the constrained flags emitted by
// stream.mediaMTXUpdate. It does not execute or otherwise interpret the
// command as a shell expression. Invalid or ambiguous optional values are
// omitted so callers can fall back to the currently selected device mode.
func parseAnalogRunOnDemand(command string) (analogRunOnDemandSettings, bool) {
	if len(command) == 0 || len(command) > 16<<10 {
		return analogRunOnDemandSettings{}, false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 || fields[0] != "/usr/bin/ffmpeg" || !hasCommandFlagValue(fields, "-f", "v4l2") {
		return analogRunOnDemandSettings{}, false
	}

	devicePath, ok := uniqueCommandFlagValue(fields, "-i")
	if !ok || !analogDevicePathPattern.MatchString(devicePath) {
		return analogRunOnDemandSettings{}, false
	}
	inputFormat, ok := uniqueCommandFlagValue(fields, "-input_format")
	if !ok {
		return analogRunOnDemandSettings{}, false
	}
	settings := analogRunOnDemandSettings{DevicePath: devicePath}
	switch strings.ToLower(inputFormat) {
	case "mjpeg":
		settings.PixelFormat = "MJPG"
	case "yuyv422":
		settings.PixelFormat = "YUYV"
	default:
		return analogRunOnDemandSettings{}, false
	}
	if resolution, found := uniqueCommandFlagValue(fields, "-video_size"); found && analogResolutionPattern.MatchString(resolution) {
		settings.Resolution = resolution
	}
	if fps, found := uniqueCommandFlagValue(fields, "-framerate"); found && analogFPSPattern.MatchString(fps) {
		settings.FPS = fps
	}
	return settings, true
}

func uniqueCommandFlagValue(fields []string, flag string) (string, bool) {
	value := ""
	found := false
	for index := 0; index < len(fields); index++ {
		if fields[index] != flag {
			continue
		}
		if found || index+1 >= len(fields) {
			return "", false
		}
		value = fields[index+1]
		found = true
	}
	return value, found
}

func hasCommandFlagValue(fields []string, flag, value string) bool {
	for index := 0; index+1 < len(fields); index++ {
		if fields[index] == flag && fields[index+1] == value {
			return true
		}
	}
	return false
}

func rtspWithoutCredentials(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	parsed.User = nil
	return parsed.String()
}
