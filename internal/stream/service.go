package stream

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"DronService/internal/mediamtx"
)

type State string

const (
	StateConfigured State = "configured"
	StateIdle       State = "idle"
	StateStarting   State = "starting"
	StateOnline     State = "online"
	StateError      State = "error"
)

// Stream is the DronService-owned representation exposed through the API.
type Stream struct {
	Name          string `json:"name"`
	State         State  `json:"state"`
	Readers       int    `json:"readers"`
	InboundBytes  int64  `json:"inboundBytes"`
	OutboundBytes int64  `json:"outboundBytes"`
}

type Config struct {
	Name           string `json:"name"`
	Source         string `json:"source"`
	SourceID       string `json:"sourceId,omitempty"`
	SourceType     string `json:"sourceType,omitempty"`
	SourceName     string `json:"sourceName,omitempty"`
	SourceDetail   string `json:"sourceDetail,omitempty"`
	SourceOnDemand bool   `json:"sourceOnDemand"`
	HasCredentials bool   `json:"hasCredentials"`
	RTSPPath       string `json:"rtspPath"`
	RunOnDemand    string `json:"-"`
}

type Source struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Detail      string `json:"detail,omitempty"`
	Input       string `json:"-"`
	DevicePath  string `json:"-"`
	PixelFormat string `json:"-"`
	Resolution  string `json:"-"`
	FPS         string `json:"-"`
}

type ListResponse struct {
	Streams []Stream `json:"streams"`
}

func (s *Service) ListConfigs(ctx context.Context) ([]Config, error) {
	paths, err := s.mediaMTX.ListConfigPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("list stream configurations: %w", err)
	}
	configs := make([]Config, 0, len(paths))
	for _, path := range paths {
		configs = append(configs, Config{Name: path.Name, Source: sourceWithoutCredentials(path.Source), SourceOnDemand: path.SourceOnDemand, HasCredentials: hasURLCredentials(path.Source), RunOnDemand: path.RunOnDemand})
	}
	return configs, nil
}

func (s *Service) ApplySource(ctx context.Context, config Config, source Source, existingName string) error {
	pathUpdate, err := mediaMTXUpdate(config.Name, source)
	if err != nil {
		return err
	}
	if existingName == "" {
		return s.mediaMTX.AddConfigPath(ctx, config.Name, pathUpdate)
	}
	if existingName == config.Name {
		return s.mediaMTX.PatchConfigPath(ctx, existingName, pathUpdate)
	}
	if !streamNamePattern.MatchString(existingName) {
		return fmt.Errorf("invalid existing stream name")
	}
	if err := s.mediaMTX.AddConfigPath(ctx, config.Name, pathUpdate); err != nil {
		return fmt.Errorf("add renamed stream configuration: %w", err)
	}
	if err := s.mediaMTX.DeleteConfigPath(ctx, existingName); err != nil {
		_ = s.mediaMTX.DeleteConfigPath(ctx, config.Name)
		return fmt.Errorf("delete previous stream configuration: %w", err)
	}
	return nil
}

var (
	streamNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)
	devicePathPattern = regexp.MustCompile(`^/dev/video[0-9]+$`)
	resolutionPattern = regexp.MustCompile(`^[0-9]{2,5}x[0-9]{2,5}$`)
	fpsPattern        = regexp.MustCompile(`^[0-9]{1,3}(?:\.[0-9]+)?$`)
)

func mediaMTXUpdate(streamName string, source Source) (mediamtx.PathConfigUpdate, error) {
	if !streamNamePattern.MatchString(streamName) {
		return mediamtx.PathConfigUpdate{}, fmt.Errorf("invalid stream name")
	}
	switch source.Type {
	case "ip":
		parsed, err := url.Parse(source.Input)
		if err != nil || (parsed.Scheme != "rtsp" && parsed.Scheme != "rtsps") || parsed.Host == "" {
			return mediamtx.PathConfigUpdate{}, fmt.Errorf("IP camera has no valid RTSP source")
		}
		return mediamtx.PathConfigUpdate{Source: source.Input, SourceOnDemand: true}, nil
	case "analog":
		if !devicePathPattern.MatchString(source.DevicePath) || !resolutionPattern.MatchString(source.Resolution) || !fpsPattern.MatchString(source.FPS) {
			return mediamtx.PathConfigUpdate{}, fmt.Errorf("analog camera has an invalid V4L2 configuration")
		}
		inputFormat := strings.ToLower(source.PixelFormat)
		switch strings.ToUpper(source.PixelFormat) {
		case "MJPG", "MJPEG":
			inputFormat = "mjpeg"
		case "YUYV", "YUY2":
			inputFormat = "yuyv422"
		default:
			if !regexp.MustCompile(`^[a-zA-Z0-9_]+$`).MatchString(source.PixelFormat) {
				return mediamtx.PathConfigUpdate{}, fmt.Errorf("analog camera pixel format is invalid")
			}
		}
		fpsValue, _ := strconv.ParseFloat(source.FPS, 64)
		gop := int(fpsValue + 0.5)
		if gop < 1 {
			gop = 25
		}
		command := fmt.Sprintf("/usr/bin/ffmpeg -hide_banner -loglevel warning -thread_queue_size 32 -f v4l2 -input_format %s -video_size %s -framerate %s -i %s -an -c:v libx264 -preset ultrafast -tune zerolatency -profile:v baseline -pix_fmt yuv420p -bf 0 -refs 1 -g %d -keyint_min %d -sc_threshold 0 -x264-params repeat-headers=1 -flush_packets 1 -f rtsp -rtsp_transport tcp rtsp://127.0.0.1:554/%s", inputFormat, source.Resolution, source.FPS, source.DevicePath, gop, gop, streamName)
		return mediamtx.PathConfigUpdate{Source: "publisher", RunOnDemand: command, RunOnDemandRestart: true, RunOnDemandStartTimeout: "15s", RunOnDemandCloseAfter: "2s"}, nil
	default:
		return mediamtx.PathConfigUpdate{}, fmt.Errorf("unsupported stream source type")
	}
}

func (s *Service) AddConfig(ctx context.Context, config Config) error {
	return s.mediaMTX.AddConfigPath(ctx, config.Name, mediamtx.PathConfigUpdate{Source: config.Source, SourceOnDemand: config.SourceOnDemand})
}

func (s *Service) UpdateConfig(ctx context.Context, config Config) error {
	paths, err := s.mediaMTX.ListConfigPaths(ctx)
	if err != nil {
		return fmt.Errorf("get stream configuration before update: %w", err)
	}
	for _, path := range paths {
		if path.Name == config.Name && hasURLCredentials(path.Source) && sourceWithoutCredentials(path.Source) == config.Source {
			config.Source = path.Source
			break
		}
	}
	return s.mediaMTX.PatchConfigPath(ctx, config.Name, mediamtx.PathConfigUpdate{Source: config.Source, SourceOnDemand: config.SourceOnDemand})
}

func (s *Service) DeleteConfig(ctx context.Context, name string) error {
	return s.mediaMTX.DeleteConfigPath(ctx, name)
}

func sourceWithoutCredentials(source string) string {
	parsed, err := url.Parse(source)
	if err != nil || parsed.User == nil {
		return source
	}
	parsed.User = nil
	return parsed.String()
}

func hasURLCredentials(source string) bool {
	parsed, err := url.Parse(source)
	return err == nil && parsed.User != nil
}

type Service struct {
	mediaMTX *mediamtx.Client
}

func NewService(mediaMTX *mediamtx.Client) *Service {
	return &Service{mediaMTX: mediaMTX}
}

func (s *Service) List(ctx context.Context) ([]Stream, error) {
	paths, err := s.mediaMTX.ListPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("list streams: %w", err)
	}

	streams := make([]Stream, 0, len(paths))
	for _, path := range paths {
		streams = append(streams, fromMediaMTXPath(path))
	}

	return streams, nil
}

func fromMediaMTXPath(path mediamtx.Path) Stream {
	state := StateIdle
	if path.Online || path.Ready || path.Available {
		state = StateOnline
	}

	return Stream{
		Name:          path.Name,
		State:         state,
		Readers:       len(path.Readers),
		InboundBytes:  path.InboundBytes,
		OutboundBytes: path.OutboundBytes,
	}
}
