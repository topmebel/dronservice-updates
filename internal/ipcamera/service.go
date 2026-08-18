package ipcamera

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrCameraNotFound       = errors.New("IP camera not found")
	ErrInvalidPreviewStream = errors.New("invalid camera preview stream")
)

type Camera struct {
	ID                   string               `json:"id"`
	Name                 string               `json:"name,omitempty"`
	Address              string               `json:"address"`
	MAC                  string               `json:"mac,omitempty"`
	Model                string               `json:"model,omitempty"`
	Manufacturer         string               `json:"manufacturer,omitempty"`
	Vendor               string               `json:"vendor,omitempty"`
	DeviceClass          string               `json:"deviceClass,omitempty"`
	Serial               string               `json:"serial,omitempty"`
	Firmware             string               `json:"firmware,omitempty"`
	MachineName          string               `json:"machineName,omitempty"`
	SubnetMask           string               `json:"subnetMask,omitempty"`
	Gateway              string               `json:"gateway,omitempty"`
	HTTPPort             uint16               `json:"httpPort,omitempty"`
	ServicePort          uint16               `json:"servicePort,omitempty"`
	DHCPEnabled          *bool                `json:"dhcpEnabled,omitempty"`
	Protocol             string               `json:"protocol,omitempty"`
	SourceAddress        string               `json:"sourceAddress,omitempty"`
	LastSeen             time.Time            `json:"lastSeen,omitempty"`
	InitializationStatus InitializationStatus `json:"initializationStatus"`
	Username             string               `json:"username,omitempty"`
	HasPassword          bool                 `json:"hasPassword"`
	MainStreamPath       string               `json:"mainStreamPath,omitempty"`
	SubStreamPath        string               `json:"subStreamPath,omitempty"`
	MainStream           VideoStream          `json:"mainStream"`
	SubStream            VideoStream          `json:"subStream"`
	Online               bool                 `json:"online"`
	Use                  bool                 `json:"use"`
}

type InitializationStatus string

const (
	InitializationUnknown   InitializationStatus = "unknown"
	InitializationRequired  InitializationStatus = "uninitialized"
	InitializationCompleted InitializationStatus = "initialized"
)

type VideoStream struct {
	Resolution  string `json:"resolution,omitempty"`
	FPS         string `json:"fps,omitempty"`
	BitrateKbps uint32 `json:"bitrateKbps,omitempty"`
}

type SaveRequest struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Address        string `json:"address"`
	SubnetMask     string `json:"subnetMask"`
	Gateway        string `json:"gateway"`
	Manufacturer   string `json:"manufacturer"`
	Model          string `json:"model"`
	Username       string `json:"username"`
	Password       string `json:"password"`
	MainStreamPath string `json:"mainStreamPath"`
	SubStreamPath  string `json:"subStreamPath"`
	Use            bool   `json:"use"`
	dhcpEnabled    *bool
}

type persistedCamera struct {
	ID                   string               `json:"id"`
	Name                 string               `json:"name"`
	Address              string               `json:"address"`
	Username             string               `json:"username"`
	Password             string               `json:"password"`
	MainStreamPath       string               `json:"mainStreamPath,omitempty"`
	SubStreamPath        string               `json:"subStreamPath,omitempty"`
	LegacyRTSPPath       string               `json:"rtspPath,omitempty"`
	MAC                  string               `json:"mac,omitempty"`
	Model                string               `json:"model,omitempty"`
	Manufacturer         string               `json:"manufacturer,omitempty"`
	Vendor               string               `json:"vendor,omitempty"`
	DeviceClass          string               `json:"deviceClass,omitempty"`
	Serial               string               `json:"serial,omitempty"`
	Firmware             string               `json:"firmware,omitempty"`
	MachineName          string               `json:"machineName,omitempty"`
	SubnetMask           string               `json:"subnetMask,omitempty"`
	Gateway              string               `json:"gateway,omitempty"`
	HTTPPort             uint16               `json:"httpPort,omitempty"`
	ServicePort          uint16               `json:"servicePort,omitempty"`
	DHCPEnabled          *bool                `json:"dhcpEnabled,omitempty"`
	Protocol             string               `json:"protocol,omitempty"`
	SourceAddress        string               `json:"sourceAddress,omitempty"`
	LastSeen             time.Time            `json:"lastSeen,omitempty"`
	InitializationStatus InitializationStatus `json:"initializationStatus,omitempty"`
	MainStream           VideoStream          `json:"mainStream,omitempty"`
	SubStream            VideoStream          `json:"subStream,omitempty"`
	Use                  bool                 `json:"use"`
}

type Service struct {
	mu          sync.RWMutex
	discoveryMu sync.Mutex
	filePath    string
	cameras     map[string]persistedCamera
	online      map[string]bool
	discovery   DahuaDiscoverOptions
	dahuaCGI    *dahuaCGIClient
}

func NewService(dataDir string, discovery DahuaDiscoverOptions) (*Service, error) {
	service := &Service{filePath: filepath.Join(dataDir, "ip-cameras.json"), cameras: make(map[string]persistedCamera), online: make(map[string]bool), discovery: discovery, dahuaCGI: newDahuaCGIClient()}
	data, err := os.ReadFile(service.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return service, nil
		}
		return nil, fmt.Errorf("read IP camera configurations: %w", err)
	}
	if err := json.Unmarshal(data, &service.cameras); err != nil {
		return nil, fmt.Errorf("decode IP camera configurations: %w", err)
	}
	return service, nil
}

func (s *Service) Discover(ctx context.Context) ([]Camera, error) {
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()

	found, err := DiscoverCameras(ctx, DiscoverOptions{
		InterfaceName: s.discovery.InterfaceName,
		Timeout:       s.discovery.Timeout,
		Vendors:       []string{"dahua", "unv"},
		EnableARP:     true,
	})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]persistedCamera, len(found)+len(s.cameras))
	for id, saved := range s.cameras {
		next[id] = saved
	}
	online := make(map[string]bool, len(found))
	now := time.Now().UTC()
	for _, device := range found {
		id := discoveredDeviceKey(device)
		next[id] = updateGenericDiscoveredCamera(next[id], id, device, now)
		online[id] = true
	}
	if len(found) > 0 {
		if err := writeJSONFile(s.filePath, next); err != nil {
			return nil, err
		}
		s.cameras = next
	}
	s.online = online
	return camerasFromPersisted(next, online), nil
}

func discoveredDeviceKey(device DiscoveredDevice) string {
	if mac := normalizeMAC(device.MAC); mac != "" {
		return "mac:" + mac
	}
	return "ip:" + device.IP.String() + "|serial:" + device.SerialNumber
}

func updateGenericDiscoveredCamera(saved persistedCamera, id string, device DiscoveredDevice, seenAt time.Time) persistedCamera {
	manufacturerSource := device.Manufacturer
	if manufacturerSource == "" {
		manufacturerSource = device.Vendor
	}
	manufacturer := canonicalManufacturer(manufacturerSource)
	mainStream, subStream := defaultRTSPPaths(manufacturer, device.IP)
	saved.ID, saved.Address, saved.MAC = id, device.IP.String(), normalizeMAC(device.MAC)
	saved.Manufacturer, saved.Vendor = manufacturer, device.Vendor
	if device.Model != "" {
		saved.Model = device.Model
	}
	if device.DeviceType != "" {
		saved.DeviceClass = device.DeviceType
	}
	if device.SerialNumber != "" {
		saved.Serial = device.SerialNumber
	}
	if device.FirmwareVersion != "" {
		saved.Firmware = device.FirmwareVersion
	}
	if device.DeviceName != "" {
		saved.MachineName = device.DeviceName
	}
	if device.SubnetMask != nil {
		saved.SubnetMask = net.IP(device.SubnetMask).String()
	} else if saved.SubnetMask == "<nil>" {
		saved.SubnetMask = ""
	}
	if device.Gateway != nil {
		saved.Gateway = device.Gateway.String()
	} else if saved.Gateway == "<nil>" {
		saved.Gateway = ""
	}
	if device.HTTPPort != 0 {
		saved.HTTPPort = device.HTTPPort
	}
	if device.ServicePort != 0 {
		saved.ServicePort = device.ServicePort
	}
	saved.Protocol = strings.Join(device.Protocols, ", ")
	if device.SourceAddress != nil {
		saved.SourceAddress = device.SourceAddress.String()
	}
	saved.LastSeen = seenAt
	saved.InitializationStatus = initializationAfterDiscovery(saved.InitializationStatus, device.InitializationStatus)
	if saved.MainStreamPath == "" {
		saved.MainStreamPath = mainStream
	} else {
		saved.MainStreamPath = replaceURLHost(saved.MainStreamPath, device.IP.String())
	}
	if saved.SubStreamPath == "" {
		saved.SubStreamPath = subStream
	} else {
		saved.SubStreamPath = replaceURLHost(saved.SubStreamPath, device.IP.String())
	}
	return saved
}

func (s *Service) CheckStatus(ctx context.Context) ([]Camera, error) {
	s.discoveryMu.Lock()
	defer s.discoveryMu.Unlock()
	s.mu.RLock()
	expectedDahua := make(map[string]struct{})
	expectedUNV := make(map[string]net.IP)
	for id, saved := range s.cameras {
		if saved.Manufacturer == "Dahua" || saved.Protocol == "DHIP" {
			expectedDahua[id] = struct{}{}
		}
		if saved.Manufacturer == "UNV" || strings.Contains(saved.Protocol, "UNV-") {
			if ip := net.ParseIP(saved.Address).To4(); ip != nil {
				expectedUNV[id] = ip
			}
		}
	}
	s.mu.RUnlock()
	if len(expectedDahua) == 0 && len(expectedUNV) == 0 {
		s.mu.Lock()
		s.online = make(map[string]bool)
		result := camerasFromPersisted(s.cameras, s.online)
		s.mu.Unlock()
		return result, nil
	}
	statusOptions := s.discovery
	statusOptions.Timeout = time.Second
	statusOptions.IncludeLegacy = false
	type statusResult struct {
		dahua []DahuaDevice
		unv   []DiscoveredDevice
		err   error
		kind  string
	}
	resultCount := 0
	results := make(chan statusResult, 2)
	if len(expectedDahua) > 0 {
		resultCount++
		go func() {
			found, err := discoverKnownDahua(ctx, statusOptions, expectedDahua)
			results <- statusResult{dahua: found, err: err, kind: "Dahua"}
		}()
	}
	if len(expectedUNV) > 0 {
		resultCount++
		go func() {
			found, err := discoverKnownUNVARP(ctx, statusOptions.InterfaceName, statusOptions.Timeout, expectedUNV)
			results <- statusResult{unv: found, err: err, kind: "UNV"}
		}()
	}
	var foundDahua []DahuaDevice
	var foundUNV []DiscoveredDevice
	for i := 0; i < resultCount; i++ {
		result := <-results
		if result.err != nil {
			log.Printf("check %s camera status: %v", result.kind, result.err)
			continue
		}
		foundDahua = append(foundDahua, result.dahua...)
		foundUNV = append(foundUNV, result.unv...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make(map[string]persistedCamera, len(s.cameras))
	for id, saved := range s.cameras {
		next[id] = saved
	}
	online := make(map[string]bool, len(next))
	now := time.Now().UTC()
	updated := false
	for _, device := range foundDahua {
		id := deviceKey(device)
		saved, exists := next[id]
		if !exists {
			continue
		}
		next[id] = updateDiscoveredCamera(saved, id, device, now)
		online[id] = true
		updated = true
	}
	for _, device := range foundUNV {
		id := discoveredDeviceKey(device)
		saved, exists := next[id]
		if !exists {
			continue
		}
		saved.LastSeen = now
		next[id] = saved
		online[id] = true
		updated = true
	}
	if updated {
		if err := writeJSONFile(s.filePath, next); err != nil {
			return nil, err
		}
		s.cameras = next
	}
	s.online = online
	return camerasFromPersisted(next, online), nil
}

func updateDiscoveredCamera(saved persistedCamera, id string, device DahuaDevice, seenAt time.Time) persistedCamera {
	manufacturer := detectManufacturer(device)
	mainStream, subStream := defaultRTSPPaths(manufacturer, device.IP)
	saved.ID, saved.Address, saved.MAC, saved.Model = id, device.IP.String(), device.MAC, device.Model
	saved.Manufacturer, saved.Vendor, saved.DeviceClass = manufacturer, device.Vendor, device.DeviceClass
	saved.Serial, saved.Firmware, saved.MachineName = device.SerialNumber, device.FirmwareVersion, device.MachineName
	saved.SubnetMask, saved.Gateway = net.IP(device.SubnetMask).String(), device.Gateway.String()
	saved.HTTPPort, saved.ServicePort, saved.DHCPEnabled, saved.Protocol = device.HTTPPort, device.ServicePort, device.DHCPEnabled, device.Protocol
	if device.SourceAddress != nil {
		saved.SourceAddress = device.SourceAddress.String()
	}
	saved.LastSeen = seenAt
	saved.InitializationStatus = initializationAfterDiscovery(saved.InitializationStatus, device.InitializationStatus)
	if saved.MainStreamPath == "" {
		saved.MainStreamPath = mainStream
	} else {
		saved.MainStreamPath = replaceURLHost(saved.MainStreamPath, device.IP.String())
	}
	if saved.SubStreamPath == "" {
		saved.SubStreamPath = subStream
	} else {
		saved.SubStreamPath = replaceURLHost(saved.SubStreamPath, device.IP.String())
	}
	return saved
}

func (s *Service) Monitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	for {
		startedAt := time.Now()
		if _, err := s.CheckStatus(ctx); err != nil && ctx.Err() == nil {
			log.Printf("monitor IP cameras: %v", err)
		}
		wait := interval - time.Since(startedAt)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Service) List() []Camera {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return camerasFromPersisted(s.cameras, s.online)
}

func (s *Service) RefreshVideoStreams(ctx context.Context, id string) (Camera, error) {
	s.mu.RLock()
	saved, exists := s.cameras[id]
	online := s.online[id]
	s.mu.RUnlock()
	if !exists {
		return Camera{}, ErrCameraNotFound
	}
	if saved.InitializationStatus == InitializationRequired {
		return Camera{}, ErrDahuaInitializationRequired
	}
	if saved.Manufacturer != "Dahua" && saved.Protocol != "DHIP" {
		return cameraFromPersisted(saved, online), nil
	}
	if strings.TrimSpace(saved.Username) == "" || saved.Password == "" {
		return Camera{}, ErrDahuaCredentials
	}

	mainStream, subStream, err := s.dahuaCGI.VideoStreams(ctx, saved.Address, saved.HTTPPort, saved.Username, saved.Password)
	if err != nil {
		return Camera{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists := s.cameras[id]
	if !exists {
		return Camera{}, ErrCameraNotFound
	}
	next := make(map[string]persistedCamera, len(s.cameras))
	for cameraID, camera := range s.cameras {
		next[cameraID] = camera
	}
	current.MainStream, current.SubStream = mainStream, subStream
	current.InitializationStatus = InitializationCompleted
	next[id] = current
	if err := writeJSONFile(s.filePath, next); err != nil {
		return Camera{}, err
	}
	s.cameras = next
	return cameraFromPersisted(current, s.online[id]), nil
}

func camerasFromPersisted(cameras map[string]persistedCamera, online map[string]bool) []Camera {
	result := make([]Camera, 0, len(cameras))
	for id, saved := range cameras {
		result = append(result, cameraFromPersisted(saved, online[id]))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

func (s *Service) Save(request SaveRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	request, err = prepareSaveRequest(request, s.cameras[request.ID].Password)
	if err != nil {
		return err
	}
	next := make(map[string]persistedCamera, len(s.cameras)+1)
	for id, camera := range s.cameras {
		next[id] = camera
	}
	saved := next[request.ID]
	saved.ID, saved.Name, saved.Address, saved.Username, saved.Password = request.ID, request.Name, request.Address, request.Username, request.Password
	saved.Manufacturer, saved.Model = request.Manufacturer, request.Model
	if request.SubnetMask != "" {
		saved.SubnetMask = request.SubnetMask
	}
	if request.Gateway != "" {
		saved.Gateway = request.Gateway
	}
	if request.dhcpEnabled != nil {
		saved.DHCPEnabled = request.dhcpEnabled
	}
	saved.MainStreamPath, saved.SubStreamPath, saved.Use = request.MainStreamPath, request.SubStreamPath, request.Use
	next[request.ID] = saved
	if err := writeJSONFile(s.filePath, next); err != nil {
		return err
	}
	s.cameras = next
	return nil
}

func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.cameras[id]; !ok {
		return ErrCameraNotFound
	}
	next := make(map[string]persistedCamera, len(s.cameras)-1)
	for cameraID, camera := range s.cameras {
		if cameraID != id {
			next[cameraID] = camera
		}
	}
	if err := writeJSONFile(s.filePath, next); err != nil {
		return err
	}
	s.cameras = next
	delete(s.online, id)
	return nil
}

func prepareSaveRequest(request SaveRequest, savedPassword string) (SaveRequest, error) {
	request.Name, request.Username = strings.TrimSpace(request.Name), strings.TrimSpace(request.Username)
	request.Address, request.Manufacturer, request.Model = strings.TrimSpace(request.Address), canonicalManufacturer(request.Manufacturer), strings.TrimSpace(request.Model)
	request.SubnetMask, request.Gateway = strings.TrimSpace(request.SubnetMask), strings.TrimSpace(request.Gateway)
	request.MainStreamPath, request.SubStreamPath = strings.TrimSpace(request.MainStreamPath), strings.TrimSpace(request.SubStreamPath)
	ip := net.ParseIP(request.Address)
	if request.ID == "" && ip != nil {
		request.ID = "manual:" + ip.String()
	}
	if request.MainStreamPath == "" || request.SubStreamPath == "" {
		request.MainStreamPath, request.SubStreamPath = defaultRTSPPaths(request.Manufacturer, ip)
	}
	var err error
	request, err = normalizeRTSPPaths(request)
	if err != nil {
		return request, err
	}
	if ip != nil {
		request.MainStreamPath = replaceURLHost(request.MainStreamPath, ip.String())
		request.SubStreamPath = replaceURLHost(request.SubStreamPath, ip.String())
	}
	if request.ID == "" || request.Name == "" || ip == nil || request.Manufacturer == "Unknown" || request.MainStreamPath == "" || request.SubStreamPath == "" {
		return request, fmt.Errorf("camera ID, name, address and both RTSP paths are required")
	}
	if !validRTSPURL(request.MainStreamPath) || !validRTSPURL(request.SubStreamPath) {
		return request, fmt.Errorf("stream paths must be valid rtsp:// or rtsps:// URLs")
	}
	if request.Password == "" {
		request.Password = savedPassword
	}
	return request, nil
}

func (s *Service) SaveWithCameraUpdate(ctx context.Context, request SaveRequest) error {
	s.mu.RLock()
	current, exists := s.cameras[request.ID]
	s.mu.RUnlock()
	request.Address = strings.TrimSpace(request.Address)
	request.SubnetMask, request.Gateway = strings.TrimSpace(request.SubnetMask), strings.TrimSpace(request.Gateway)
	if exists && current.InitializationStatus == InitializationRequired {
		return ErrDahuaInitializationRequired
	}
	if exists && current.Manufacturer == "Dahua" {
		if request.SubnetMask == "" {
			request.SubnetMask = current.SubnetMask
		}
		if request.Gateway == "" {
			request.Gateway = current.Gateway
		}
		if err := validateIPv4Network(request.Address, request.SubnetMask, request.Gateway); err != nil {
			return err
		}
	}
	prepared, err := prepareSaveRequest(request, current.Password)
	if err != nil {
		return err
	}
	request = prepared
	if exists && current.Manufacturer == "Dahua" && (request.Address != current.Address || request.SubnetMask != current.SubnetMask || request.Gateway != current.Gateway) {
		username, password := strings.TrimSpace(request.Username), request.Password
		if username == "" {
			username = current.Username
		}
		if password == "" {
			password = current.Password
		}
		if username == "" || password == "" {
			return ErrDahuaCredentials
		}
		if err := s.dahuaCGI.ChangeIPv4(ctx, current.Address, current.HTTPPort, username, password, request.Address, request.SubnetMask, request.Gateway); err != nil {
			return err
		}
		dhcpDisabled := false
		request.dhcpEnabled = &dhcpDisabled
		request.Username, request.Password = username, password
	}
	return s.Save(request)
}

func validateIPv4Network(address, subnet, gateway string) error {
	if net.ParseIP(address).To4() == nil {
		return fmt.Errorf("camera address must be a valid IPv4 address")
	}
	if subnet != "" {
		maskIP := net.ParseIP(subnet).To4()
		if maskIP == nil {
			return fmt.Errorf("camera subnet mask must be a valid IPv4 mask")
		}
		if ones, bits := net.IPMask(maskIP).Size(); bits != 32 || ones == 0 {
			return fmt.Errorf("camera subnet mask must be contiguous")
		}
	}
	if gateway != "" && net.ParseIP(gateway).To4() == nil {
		return fmt.Errorf("camera gateway must be a valid IPv4 address")
	}
	return nil
}

func replaceURLHost(value, newAddress string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return value
	}
	port := parsed.Port()
	parsed.Host = newAddress
	if port != "" {
		parsed.Host = net.JoinHostPort(newAddress, port)
	}
	return parsed.String()
}

func cameraFromPersisted(saved persistedCamera, online bool) Camera {
	mainStream := saved.MainStreamPath
	if mainStream == "" {
		mainStream = saved.LegacyRTSPPath
	}
	return Camera{
		ID: saved.ID, Name: saved.Name, Address: saved.Address, MAC: saved.MAC, Model: saved.Model,
		Manufacturer: saved.Manufacturer, Vendor: saved.Vendor, DeviceClass: saved.DeviceClass,
		Serial: saved.Serial, Firmware: saved.Firmware, MachineName: saved.MachineName,
		SubnetMask: saved.SubnetMask, Gateway: saved.Gateway, HTTPPort: saved.HTTPPort,
		ServicePort: saved.ServicePort, DHCPEnabled: saved.DHCPEnabled, Protocol: saved.Protocol,
		SourceAddress: saved.SourceAddress, LastSeen: saved.LastSeen,
		InitializationStatus: normalizeInitializationStatus(saved.InitializationStatus), Username: saved.Username,
		HasPassword:    saved.Password != "",
		MainStreamPath: rtspURLWithoutCredentials(mainStream),
		SubStreamPath:  rtspURLWithoutCredentials(saved.SubStreamPath),
		MainStream:     saved.MainStream, SubStream: saved.SubStream,
		Online: online, Use: saved.Use,
	}
}

func normalizeInitializationStatus(status InitializationStatus) InitializationStatus {
	switch status {
	case InitializationRequired, InitializationCompleted:
		return status
	default:
		return InitializationUnknown
	}
}

func initializationAfterDiscovery(previous, observed InitializationStatus) InitializationStatus {
	observed = normalizeInitializationStatus(observed)
	if observed != InitializationUnknown {
		return observed
	}
	if normalizeInitializationStatus(previous) == InitializationCompleted {
		return InitializationCompleted
	}
	return InitializationUnknown
}

type StreamSource struct {
	ID       string
	Name     string
	Detail   string
	Kind     string
	Metadata VideoStream
	URL      string
}

// PreviewStreamSource returns a credential-bearing camera source for internal
// preview transport. Callers must never expose URL to remote clients.
func (s *Service) PreviewStreamSource(id, streamKind string) (StreamSource, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	camera, ok := s.cameras[id]
	if !ok {
		return StreamSource{}, ErrCameraNotFound
	}
	if normalizeInitializationStatus(camera.InitializationStatus) == InitializationRequired {
		return StreamSource{}, ErrDahuaInitializationRequired
	}
	streamPath, detail := "", ""
	metadata := VideoStream{}
	switch streamKind {
	case "main":
		streamPath, detail = camera.MainStreamPath, "Main stream"
		metadata = camera.MainStream
		if streamPath == "" {
			streamPath = camera.LegacyRTSPPath
		}
	case "sub":
		streamPath, detail = camera.SubStreamPath, "Sub stream"
		metadata = camera.SubStream
	default:
		return StreamSource{}, ErrInvalidPreviewStream
	}
	source := rtspURLWithCredentials(streamPath, camera.Username, camera.Password)
	if source == "" {
		return StreamSource{}, fmt.Errorf("camera has no valid %s RTSP stream", streamKind)
	}
	name := camera.Name
	if name == "" {
		name = camera.Address
	}
	return StreamSource{ID: camera.ID + ":" + streamKind, Name: name, Detail: detail, Kind: streamKind, Metadata: metadata, URL: source}, nil
}

func (s *Service) MainStreamSource(id string) (StreamSource, error) {
	return s.PreviewStreamSource(id, "main")
}

// StreamSources returns credential-bearing URLs used for MediaMTX configuration.
func (s *Service) StreamSources() []StreamSource {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]StreamSource, 0)
	for _, camera := range s.cameras {
		if !camera.Use {
			continue
		}
		name := camera.Name
		if name == "" {
			name = camera.Address
		}
		if source := rtspURLWithCredentials(camera.MainStreamPath, camera.Username, camera.Password); source != "" {
			result = append(result, StreamSource{
				ID:       camera.ID + ":main",
				Name:     name,
				Detail:   "Main stream",
				Kind:     "main",
				Metadata: camera.MainStream,
				URL:      source,
			})
		}
		if source := rtspURLWithCredentials(camera.SubStreamPath, camera.Username, camera.Password); source != "" {
			result = append(result, StreamSource{
				ID:       camera.ID + ":sub",
				Name:     name,
				Detail:   "Sub stream",
				Kind:     "sub",
				Metadata: camera.SubStream,
				URL:      source,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func rtspURLWithCredentials(value, username, password string) string {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "rtsp" && parsed.Scheme != "rtsps") || parsed.Host == "" {
		return ""
	}
	if username != "" {
		if password != "" {
			parsed.User = url.UserPassword(username, password)
		} else {
			parsed.User = url.User(username)
		}
	}
	return parsed.String()
}

func rtspURLWithoutCredentials(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "rtsp" && parsed.Scheme != "rtsps") || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	return parsed.String()
}

func normalizeRTSPPaths(request SaveRequest) (SaveRequest, error) {
	type credentials struct {
		username    string
		password    string
		hasUsername bool
		hasPassword bool
	}

	normalize := func(value string) (string, credentials, error) {
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "rtsp" && parsed.Scheme != "rtsps") || parsed.Host == "" {
			return "", credentials{}, fmt.Errorf("stream paths must be valid rtsp:// or rtsps:// URLs")
		}
		result := credentials{}
		if parsed.User != nil {
			result.username = parsed.User.Username()
			result.hasUsername = true
			result.password, result.hasPassword = parsed.User.Password()
		}
		parsed.User = nil
		return parsed.String(), result, nil
	}

	mainPath, mainCredentials, err := normalize(request.MainStreamPath)
	if err != nil {
		return request, err
	}
	subPath, subCredentials, err := normalize(request.SubStreamPath)
	if err != nil {
		return request, err
	}
	for _, pathCredentials := range []credentials{mainCredentials, subCredentials} {
		if pathCredentials.hasUsername {
			if request.Username != "" && request.Username != pathCredentials.username {
				return request, fmt.Errorf("RTSP path username does not match camera username")
			}
			request.Username = pathCredentials.username
		}
		if pathCredentials.hasPassword {
			if request.Password != "" && request.Password != pathCredentials.password {
				return request, fmt.Errorf("RTSP path password does not match camera password")
			}
			request.Password = pathCredentials.password
		}
	}
	request.MainStreamPath, request.SubStreamPath = mainPath, subPath
	return request, nil
}

func detectManufacturer(device DahuaDevice) string {
	combined := strings.ToLower(device.Manufacturer + " " + device.Vendor + " " + device.Model)
	if strings.Contains(combined, "dahua") || strings.HasPrefix(strings.ToUpper(device.Model), "DH-") {
		return "Dahua"
	}
	if strings.Contains(combined, "hikvision") || strings.HasPrefix(strings.ToUpper(device.Model), "DS-") {
		return "Hikvision"
	}
	if strings.Contains(combined, "uniview") || strings.Contains(combined, "unv") {
		return "UNV"
	}
	if device.Manufacturer != "" {
		return canonicalManufacturer(device.Manufacturer)
	}
	return canonicalManufacturer(device.Vendor)
}

func defaultRTSPPaths(manufacturer string, ip net.IP) (string, string) {
	if ip == nil {
		return "", ""
	}
	base := "rtsp://" + net.JoinHostPort(ip.String(), "554")
	switch canonicalManufacturer(manufacturer) {
	case "Dahua":
		return base + "/cam/realmonitor?channel=1&subtype=0", base + "/cam/realmonitor?channel=1&subtype=1"
	case "Hikvision":
		return base + "/Streaming/Channels/101", base + "/Streaming/Channels/102"
	case "UNV":
		return base + "/unicast/c1/s0/live", base + "/unicast/c1/s1/live"
	default:
		return "", ""
	}
}

func canonicalManufacturer(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dahua":
		return "Dahua"
	case "hikvision", "hik":
		return "Hikvision"
	case "unv", "uniview":
		return "UNV"
	default:
		return "Unknown"
	}
}

func validRTSPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "rtsp" || parsed.Scheme == "rtsps") && parsed.Host != "" && parsed.User == nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode IP cameras: %w", err)
	}
	file, err := os.CreateTemp(filepath.Dir(path), "ip-cameras-*.tmp")
	if err != nil {
		return fmt.Errorf("create IP camera configuration: %w", err)
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fmt.Errorf("write IP camera configuration: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync IP camera configuration: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close IP camera configuration: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("replace IP camera configuration: %w", err)
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open IP camera configuration directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("sync IP camera configuration directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close IP camera configuration directory: %w", err)
	}
	return nil
}
