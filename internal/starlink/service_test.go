package starlink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/b0ch3nski/go-starlink/starlink/model/device"
)

func TestStatusReportsDishMetricsAndStarlinkProvider(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"org":"AS14593 Space Exploration Technologies Corporation","hostname":"customer.example.starlink.com"}`))
	}))
	defer provider.Close()

	service := &Service{
		dishAddress: defaultDishAddress,
		dishStatus: func(context.Context) (*device.DishGetStatusResponse, error) {
			return &device.DishGetStatusResponse{
				DeviceInfo:            &device.DeviceInfo{HardwareVersion: "rev4", SoftwareVersion: "firmware"},
				DownlinkThroughputBps: 12_500_000,
				UplinkThroughputBps:   2_250_000,
				PopPingLatencyMs:      31.25,
				PopPingDropRate:       0.0125,
			}, nil
		},
		http:        provider.Client(),
		providerURL: provider.URL,
		now:         func() time.Time { return time.Unix(100, 0) },
	}

	status := service.Status(context.Background(), true)
	if !status.Detected || !status.Reachable || !status.InternetViaStarlink || status.State != "online" {
		t.Fatalf("status = %+v", status)
	}
	if status.DownlinkMbps != 12.5 || status.UplinkMbps != 2.25 || status.PingMS != 31.25 || status.DropRatePercent != 1.25 {
		t.Fatalf("metrics = %+v", status)
	}
	if status.HardwareVersion != "rev4" || status.SoftwareVersion != "firmware" {
		t.Fatalf("device versions = %+v", status)
	}
}

func TestStatusDoesNotClaimInternetUsesDetectedDishWithOtherProvider(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"org":"AS13335 Cloudflare, Inc.","hostname":"example.net"}`))
	}))
	defer provider.Close()

	service := &Service{
		dishAddress: defaultDishAddress,
		dishStatus: func(context.Context) (*device.DishGetStatusResponse, error) {
			return &device.DishGetStatusResponse{}, nil
		},
		http:        provider.Client(),
		providerURL: provider.URL,
		now:         time.Now,
	}

	status := service.Status(context.Background(), true)
	if !status.Detected || status.InternetViaStarlink {
		t.Fatalf("status = %+v", status)
	}
}

func TestSnapshotRecordsDishAlertsAndOutage(t *testing.T) {
	service := &Service{
		dishAddress: defaultDishAddress,
		dishStatus: func(context.Context) (*device.DishGetStatusResponse, error) {
			return &device.DishGetStatusResponse{
				Outage: &device.DishOutage{Cause: device.DishOutage_NO_DOWNLINK},
				Alerts: &device.DishAlerts{NoEthernetLink: true},
			}, nil
		},
		now: func() time.Time { return time.Unix(200, 0) },
	}

	snapshot := service.Snapshot(context.Background(), false, "1h")
	if snapshot.State != "no_downlink" {
		t.Fatalf("state = %q", snapshot.State)
	}
	if len(snapshot.Alerts) == 0 || snapshot.Alerts[0] != "Нет Ethernet-линка" {
		t.Fatalf("alerts = %#v", snapshot.Alerts)
	}
	if len(snapshot.Events) == 0 {
		t.Fatal("expected connection error events")
	}
}

func TestSnapshotRecordsUnreachableDish(t *testing.T) {
	service := &Service{
		dishAddress: "192.0.2.1:9200",
		dishStatus: func(context.Context) (*device.DishGetStatusResponse, error) {
			return nil, context.DeadlineExceeded
		},
		http: &http.Client{Timeout: 50 * time.Millisecond},
		now:  func() time.Time { return time.Unix(300, 0) },
	}

	snapshot := service.Snapshot(context.Background(), false, "1h")
	if snapshot.Reachable {
		t.Fatal("dish should be unreachable")
	}
	if len(snapshot.Events) == 0 || snapshot.Events[0].Message != "Терминал Starlink недоступен" {
		t.Fatalf("events = %#v", snapshot.Events)
	}
}

func TestRebootCallsDishAndClearsCachedStatus(t *testing.T) {
	called := false
	service := &Service{
		dishReboot: func(context.Context) error {
			called = true
			return nil
		},
		cached: Status{Detected: true, CheckedAt: time.Now()},
	}

	if err := service.Reboot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("dish reboot was not called")
	}
	if !service.cached.CheckedAt.IsZero() {
		t.Fatalf("cached status was not cleared: %+v", service.cached)
	}
}
