package starlink

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/b0ch3nski/go-starlink/starlink/model/device"
)

func TestRecordHistoryPersistsMetrics(t *testing.T) {
	dir := t.TempDir()
	service := &Service{
		historyPath:       filepath.Join(dir, "starlink-history.json"),
		minuteHistoryPath: filepath.Join(dir, "starlink-history-minutes.json"),
		now:               func() time.Time { return time.Unix(100, 0) },
	}

	service.recordHistory(Status{
		Reachable:    true,
		DownlinkMbps: 12.5,
		UplinkMbps:   2.1,
		PingMS:       22,
	})
	if len(service.history) != 1 {
		t.Fatalf("history = %#v", service.history)
	}
	if len(service.minuteHistory) != 1 {
		t.Fatalf("minute history = %#v", service.minuteHistory)
	}

	service.now = func() time.Time { return time.Unix(110, 0) }
	service.recordHistory(Status{Reachable: false})
	if len(service.history) != 2 {
		t.Fatalf("history = %#v", service.history)
	}

	loaded := &Service{
		historyPath:       service.historyPath,
		minuteHistoryPath: service.minuteHistoryPath,
	}
	loaded.loadHistory()
	if len(loaded.history) != 2 || len(loaded.minuteHistory) != 1 {
		t.Fatalf("loaded history = %#v minute = %#v", loaded.history, loaded.minuteHistory)
	}
}

func TestRecordHistorySkipsDuplicateWithinInterval(t *testing.T) {
	service := &Service{
		now: func() time.Time { return time.Unix(200, 0) },
	}
	status := Status{Reachable: true, DownlinkMbps: 5, UplinkMbps: 1, PingMS: 30}
	service.recordHistory(status)
	service.now = func() time.Time { return time.Unix(202, 0) }
	service.recordHistory(status)
	if len(service.history) != 1 {
		t.Fatalf("history = %#v", service.history)
	}
}

func TestSnapshotIncludesHistory(t *testing.T) {
	dir := t.TempDir()
	service := &Service{
		dishAddress: defaultDishAddress,
		dishStatus: func(context.Context) (*device.DishGetStatusResponse, error) {
			return &device.DishGetStatusResponse{
				DownlinkThroughputBps: 8_000_000,
				UplinkThroughputBps:   1_000_000,
				PopPingLatencyMs:      25,
			}, nil
		},
		historyPath:       filepath.Join(dir, "starlink-history.json"),
		minuteHistoryPath: filepath.Join(dir, "starlink-history-minutes.json"),
		now:               func() time.Time { return time.Unix(300, 0) },
	}

	snapshot := service.Snapshot(context.Background(), false, "1h")
	if len(snapshot.History) != 1 {
		t.Fatalf("history = %#v", snapshot.History)
	}
	if snapshot.HistoryRange != "1h" {
		t.Fatalf("historyRange = %q", snapshot.HistoryRange)
	}
	if _, err := os.Stat(service.historyPath); err != nil {
		t.Fatalf("history file was not written: %v", err)
	}
}

func TestHistoryForRangeFiltersRawPoints(t *testing.T) {
	now := time.Unix(10_000, 0)
	service := &Service{
		history: []ConnectionPoint{
			{Time: now.Add(-30 * time.Minute), DownlinkMbps: 1},
			{Time: now.Add(-20 * time.Minute), DownlinkMbps: 2},
			{Time: now.Add(-5 * time.Minute), DownlinkMbps: 3},
		},
	}

	points := service.historyForRangeLocked("10m", now)
	if len(points) != 1 || points[0].DownlinkMbps != 3 {
		t.Fatalf("10m history = %#v", points)
	}

	points = service.historyForRangeLocked("1h", now)
	if len(points) != 3 {
		t.Fatalf("1h history = %#v", points)
	}
}

func TestHistoryForRangeUsesMinuteBucketsForDayAndWeek(t *testing.T) {
	now := time.Unix(20_000, 0)
	service := &Service{
		minuteHistory: []minuteBucket{
			{Time: now.Add(-48 * time.Hour), DownlinkMbps: 1, Samples: 1},
			{Time: now.Add(-2 * time.Hour), DownlinkMbps: 2, Samples: 1},
			{Time: now.Add(-30 * time.Minute), DownlinkMbps: 3, Samples: 1},
		},
	}

	day := service.historyForRangeLocked("24h", now)
	if len(day) != 2 {
		t.Fatalf("24h history = %#v", day)
	}

	week := service.historyForRangeLocked("7d", now)
	if len(week) == 0 {
		t.Fatal("expected hourly aggregates for week range")
	}
}

func TestParseHistoryRangeDefaultsToHour(t *testing.T) {
	duration, key := ParseHistoryRange("")
	if key != "1h" || duration != time.Hour {
		t.Fatalf("range = %s %s", key, duration)
	}
}
