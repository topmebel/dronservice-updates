package main

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"DronService/internal/deviceconfig"
	"DronService/internal/ipcamera"
	"DronService/internal/stream"
	"DronService/internal/v4l2"
)

type streamSourceCatalog struct {
	scanner   *v4l2.Scanner
	devices   *deviceconfig.Store
	ipCameras *ipcamera.Service
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
		sources = append(sources, stream.Source{ID: "ip:" + camera.ID, Type: "ip", Name: camera.Name, Detail: camera.Detail, Input: camera.URL})
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

func decorateStreamConfigs(configs []stream.Config, sources []stream.Source, publicRTSPBase string) []stream.Config {
	for index := range configs {
		configs[index].RTSPPath = strings.TrimRight(publicRTSPBase, "/") + "/" + configs[index].Name
		for _, source := range sources {
			matches := source.Type == "analog" && strings.Contains(configs[index].RunOnDemand, " "+source.DevicePath+" ")
			if source.Type == "ip" {
				matches = rtspWithoutCredentials(source.Input) == configs[index].Source
			}
			if matches {
				configs[index].SourceID = source.ID
				configs[index].SourceType = source.Type
				configs[index].SourceName = source.Name
				configs[index].SourceDetail = source.Detail
				break
			}
		}
	}
	return configs
}

func rtspWithoutCredentials(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	parsed.User = nil
	return parsed.String()
}
