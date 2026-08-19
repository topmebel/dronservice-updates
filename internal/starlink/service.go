package starlink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	starlinkclient "github.com/b0ch3nski/go-starlink/starlink"
	"github.com/b0ch3nski/go-starlink/starlink/model/device"
)

const (
	defaultDishAddress = "192.168.100.1:9200"
	defaultProviderURL = "https://ipinfo.io/json"
)

type Status struct {
	Detected            bool      `json:"detected"`
	Reachable           bool      `json:"reachable"`
	InternetViaStarlink bool      `json:"internetViaStarlink"`
	Topology            string    `json:"topology,omitempty"`
	State               string    `json:"state,omitempty"`
	HardwareVersion     string    `json:"hardwareVersion,omitempty"`
	SoftwareVersion     string    `json:"softwareVersion,omitempty"`
	DownlinkMbps        float64   `json:"downlinkMbps,omitempty"`
	UplinkMbps          float64   `json:"uplinkMbps,omitempty"`
	PingMS              float64   `json:"pingMs,omitempty"`
	DropRatePercent     float64   `json:"dropRatePercent,omitempty"`
	CheckedAt           time.Time `json:"checkedAt"`
}

type Snapshot struct {
	Status
	Alerts       []string          `json:"alerts"`
	Events       []Event           `json:"events"`
	History      []ConnectionPoint `json:"history"`
	HistoryRange string            `json:"historyRange"`
}

type Service struct {
	mu                sync.Mutex
	dishAddress       string
	dishStatus        func(context.Context) (*device.DishGetStatusResponse, error)
	dishReboot        func(context.Context) error
	http              *http.Client
	providerURL       string
	now               func() time.Time
	eventsPath        string
	historyPath       string
	minuteHistoryPath string
	cached            Status
	alerts            []string
	events            []Event
	history           []ConnectionPoint
	minuteHistory     []minuteBucket
	providerKnown     bool
	providerIsDish    bool
	providerAt        time.Time
}

func NewService(dataDir string) (*Service, error) {
	client, err := starlinkclient.NewClient(context.Background(), defaultDishAddress)
	if err != nil {
		return nil, fmt.Errorf("prepare Starlink client: %w", err)
	}
	service := &Service{
		dishAddress: defaultDishAddress,
		dishStatus:  client.WithTimeout(3 * time.Second).Status,
		dishReboot:  client.WithTimeout(5 * time.Second).Reboot,
		http:        &http.Client{Timeout: 4 * time.Second},
		providerURL: defaultProviderURL,
		now:         time.Now,
	}
	if strings.TrimSpace(dataDir) != "" {
		service.eventsPath = filepath.Join(dataDir, "starlink-events.json")
		service.historyPath = filepath.Join(dataDir, "starlink-history.json")
		service.minuteHistoryPath = filepath.Join(dataDir, "starlink-history-minutes.json")
		service.loadEvents()
		service.loadHistory()
	}
	return service, nil
}

func (s *Service) Reboot(ctx context.Context) error {
	rebootCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if err := s.dishReboot(rebootCtx); err != nil {
		s.mu.Lock()
		s.recordEvent("error", "Не удалось перезагрузить терминал Starlink")
		s.mu.Unlock()
		return fmt.Errorf("reboot Starlink terminal: %w", err)
	}
	s.mu.Lock()
	s.cached = Status{}
	s.recordEvent("warning", "Отправлена команда перезагрузки терминала Starlink")
	s.mu.Unlock()
	return nil
}

func (s *Service) Snapshot(ctx context.Context, internetOnline bool, historyRange string) Snapshot {
	status := s.Status(ctx, internetOnline)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, rangeKey := ParseHistoryRange(historyRange)
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	return Snapshot{
		Status:       status,
		Alerts:       append([]string(nil), s.alerts...),
		Events:       copyEventsNewestFirst(s.events),
		History:      copyHistory(s.historyForRangeLocked(rangeKey, now)),
		HistoryRange: rangeKey,
	}
}

func (s *Service) Status(ctx context.Context, internetOnline bool) Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	if !s.cached.CheckedAt.IsZero() && now.Sub(s.cached.CheckedAt) < 8*time.Second {
		result := s.cached
		result.InternetViaStarlink = internetOnline && s.providerKnown && s.providerIsDish
		return result
	}

	result := Status{CheckedAt: now}
	checkCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	dish, err := s.dishStatus(checkCtx)
	cancel()
	if err == nil && dish != nil {
		result.Detected = true
		result.Reachable = true
		result.Topology = detectTopology(s.dishAddress)
		result.State = "online"
		if outage := dish.GetOutage(); outage != nil && outage.GetCause() != device.DishOutage_UNKNOWN {
			cause := outage.GetCause()
			result.State = strings.ToLower(strings.TrimPrefix(cause.String(), "DISH_OUTAGE_"))
			s.recordEvent(outageLevel(cause), outageMessage(cause))
		}
		result.DownlinkMbps = roundMetric(float64(dish.GetDownlinkThroughputBps()) / 1_000_000)
		result.UplinkMbps = roundMetric(float64(dish.GetUplinkThroughputBps()) / 1_000_000)
		result.PingMS = roundMetric(float64(dish.GetPopPingLatencyMs()))
		result.DropRatePercent = roundMetric(float64(dish.GetPopPingDropRate()) * 100)
		if info := dish.GetDeviceInfo(); info != nil {
			result.HardwareVersion = info.GetHardwareVersion()
			result.SoftwareVersion = info.GetSoftwareVersion()
		}
		alerts := dishAlerts(dish)
		for _, alert := range newAlertEvents(s.alerts, alerts) {
			s.recordEvent("error", alert)
		}
		s.alerts = alerts
	} else {
		s.alerts = nil
		if s.probeDishHTTP(ctx) {
			result.Detected = true
			result.Topology = detectTopology(s.dishAddress)
			s.recordEvent("warning", "Веб-интерфейс Starlink доступен, но API терминала не отвечает")
		} else {
			s.recordEvent("error", "Терминал Starlink недоступен")
		}
	}

	if internetOnline && (!s.providerKnown || now.Sub(s.providerAt) >= 5*time.Minute) {
		if isStarlink, known := s.detectProvider(ctx); known {
			s.providerKnown = true
			s.providerIsDish = isStarlink
			s.providerAt = now
		}
	}
	result.InternetViaStarlink = internetOnline && s.providerKnown && s.providerIsDish
	if result.Reachable && internetOnline && s.providerKnown && !s.providerIsDish {
		s.recordEvent("warning", "Интернет есть, но внешний адрес не принадлежит Starlink")
	}
	if result.Reachable && !internetOnline {
		s.recordEvent("error", "Терминал Starlink доступен, но интернет отсутствует")
	}
	s.recordHistory(result)
	s.cached = result
	return result
}

func (s *Service) probeDishHTTP(ctx context.Context) bool {
	host, _, err := net.SplitHostPort(s.dishAddress)
	if err != nil {
		return false
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/", nil)
	if err != nil {
		return false
	}
	response, err := s.http.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return err == nil && response.StatusCode == http.StatusOK && bytes.Contains(bytes.ToLower(body), []byte("<title>starlink</title>"))
}

func (s *Service) detectProvider(ctx context.Context) (bool, bool) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.providerURL, nil)
	if err != nil {
		return false, false
	}
	response, err := s.http.Do(request)
	if err != nil {
		return false, false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, false
	}
	var identity struct {
		Organization string `json:"org"`
		Hostname     string `json:"hostname"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&identity); err != nil {
		return false, false
	}
	organization := strings.ToUpper(strings.TrimSpace(identity.Organization))
	hostname := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(identity.Hostname), "."))
	isStarlink := strings.HasPrefix(organization, "AS14593 ") ||
		strings.HasSuffix(hostname, ".starlink.com") ||
		strings.HasSuffix(hostname, ".starlinkisp.net")
	return isStarlink, organization != "" || hostname != ""
}

func detectTopology(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ""
	}
	target := net.ParseIP(host).To4()
	if target == nil {
		return ""
	}
	connection, err := net.DialUDP("udp4", nil, &net.UDPAddr{IP: target, Port: 9200})
	if err != nil {
		return ""
	}
	defer connection.Close()
	local, ok := connection.LocalAddr().(*net.UDPAddr)
	if !ok || local.IP.To4() == nil {
		return ""
	}
	if local.IP.Mask(net.CIDRMask(24, 32)).Equal(target.Mask(net.CIDRMask(24, 32))) {
		return "direct"
	}
	return "router"
}

func roundMetric(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}
