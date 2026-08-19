package starlink

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"time"
)

const (
	rawRetention    = 2 * time.Hour
	rawMaxPoints    = 960
	minuteRetention = 7 * 24 * time.Hour
)

type ConnectionPoint struct {
	Time         time.Time `json:"time"`
	Reachable    bool      `json:"reachable"`
	DownlinkMbps float64   `json:"downlinkMbps,omitempty"`
	UplinkMbps   float64   `json:"uplinkMbps,omitempty"`
	PingMS       float64   `json:"pingMs,omitempty"`
}

type minuteBucket struct {
	Time         time.Time `json:"time"`
	Reachable    bool      `json:"reachable"`
	DownlinkMbps float64   `json:"downlinkMbps,omitempty"`
	UplinkMbps   float64   `json:"uplinkMbps,omitempty"`
	PingMS       float64   `json:"pingMs,omitempty"`
	Samples      int       `json:"samples,omitempty"`
}

func ParseHistoryRange(value string) (time.Duration, string) {
	switch value {
	case "10m":
		return 10 * time.Minute, "10m"
	case "1h", "":
		return time.Hour, "1h"
	case "24h", "1d":
		return 24 * time.Hour, "24h"
	case "7d", "1w":
		return 7 * 24 * time.Hour, "7d"
	default:
		return time.Hour, "1h"
	}
}

func HistoryRangeLabel(key string) string {
	switch key {
	case "10m":
		return "10 минут"
	case "1h":
		return "1 час"
	case "24h":
		return "сутки"
	case "7d":
		return "неделя"
	default:
		return "1 час"
	}
}

func (s *Service) loadHistory() {
	if s.historyPath == "" {
		return
	}
	data, err := os.ReadFile(s.historyPath)
	if err != nil {
		return
	}
	var history []ConnectionPoint
	if err := json.Unmarshal(data, &history); err != nil {
		return
	}
	s.history = history
	if s.minuteHistoryPath != "" {
		data, err = os.ReadFile(s.minuteHistoryPath)
		if err != nil {
			return
		}
		var minutes []minuteBucket
		if err := json.Unmarshal(data, &minutes); err == nil {
			s.minuteHistory = minutes
		}
	}
}

func (s *Service) saveHistoryLocked() {
	if s.historyPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.historyPath), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s.history)
	if err != nil {
		return
	}
	_ = os.WriteFile(s.historyPath, data, 0o600)
}

func (s *Service) saveMinuteHistoryLocked() {
	if s.minuteHistoryPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.minuteHistoryPath), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s.minuteHistory)
	if err != nil {
		return
	}
	_ = os.WriteFile(s.minuteHistoryPath, data, 0o600)
}

func historyPointEqual(a, b ConnectionPoint) bool {
	return a.Reachable == b.Reachable &&
		math.Abs(a.DownlinkMbps-b.DownlinkMbps) < 0.01 &&
		math.Abs(a.UplinkMbps-b.UplinkMbps) < 0.01 &&
		math.Abs(a.PingMS-b.PingMS) < 0.1
}

func (s *Service) recordHistory(status Status) {
	now := time.Now()
	if s.now != nil {
		now = s.now()
	}
	point := ConnectionPoint{
		Time:         now,
		Reachable:    status.Reachable,
		DownlinkMbps: status.DownlinkMbps,
		UplinkMbps:   status.UplinkMbps,
		PingMS:       status.PingMS,
	}
	if n := len(s.history); n > 0 {
		last := s.history[n-1]
		if now.Sub(last.Time) < 4*time.Second && historyPointEqual(last, point) {
			s.updateMinuteBucketLocked(now, point)
			return
		}
	}
	s.history = append(s.history, point)
	s.trimRawLocked(now)
	s.updateMinuteBucketLocked(now, point)
	s.saveHistoryLocked()
	s.saveMinuteHistoryLocked()
}

func (s *Service) trimRawLocked(now time.Time) {
	cutoff := now.Add(-rawRetention)
	start := 0
	for start < len(s.history) && s.history[start].Time.Before(cutoff) {
		start++
	}
	if start > 0 {
		s.history = append([]ConnectionPoint(nil), s.history[start:]...)
	}
	if len(s.history) > rawMaxPoints {
		s.history = append([]ConnectionPoint(nil), s.history[len(s.history)-rawMaxPoints:]...)
	}
}

func (s *Service) updateMinuteBucketLocked(now time.Time, point ConnectionPoint) {
	minute := now.Truncate(time.Minute)
	if n := len(s.minuteHistory); n > 0 && s.minuteHistory[n-1].Time.Equal(minute) {
		bucket := &s.minuteHistory[n-1]
		bucket.Samples++
		weight := float64(bucket.Samples)
		bucket.Reachable = bucket.Reachable || point.Reachable
		bucket.DownlinkMbps = ((bucket.DownlinkMbps * (weight - 1)) + point.DownlinkMbps) / weight
		bucket.UplinkMbps = ((bucket.UplinkMbps * (weight - 1)) + point.UplinkMbps) / weight
		bucket.PingMS = ((bucket.PingMS * (weight - 1)) + point.PingMS) / weight
		return
	}
	s.minuteHistory = append(s.minuteHistory, minuteBucket{
		Time:         minute,
		Reachable:    point.Reachable,
		DownlinkMbps: point.DownlinkMbps,
		UplinkMbps:   point.UplinkMbps,
		PingMS:       point.PingMS,
		Samples:      1,
	})
	s.trimMinuteHistoryLocked(now)
}

func (s *Service) trimMinuteHistoryLocked(now time.Time) {
	cutoff := now.Add(-minuteRetention)
	start := 0
	for start < len(s.minuteHistory) && s.minuteHistory[start].Time.Before(cutoff) {
		start++
	}
	if start > 0 {
		s.minuteHistory = append([]minuteBucket(nil), s.minuteHistory[start:]...)
	}
}

func (s *Service) historyForRangeLocked(rangeKey string, now time.Time) []ConnectionPoint {
	duration, key := ParseHistoryRange(rangeKey)
	since := now.Add(-duration)
	switch key {
	case "10m", "1h":
		return filterRawSince(s.history, since)
	case "24h":
		return minuteBucketsAsPoints(filterMinutesSince(s.minuteHistory, since))
	case "7d":
		return aggregateHourly(minuteBucketsAsPoints(filterMinutesSince(s.minuteHistory, since)))
	default:
		return filterRawSince(s.history, since)
	}
}

func filterRawSince(points []ConnectionPoint, since time.Time) []ConnectionPoint {
	out := make([]ConnectionPoint, 0, len(points))
	for _, point := range points {
		if !point.Time.Before(since) {
			out = append(out, point)
		}
	}
	return out
}

func filterMinutesSince(buckets []minuteBucket, since time.Time) []minuteBucket {
	out := make([]minuteBucket, 0, len(buckets))
	for _, bucket := range buckets {
		if !bucket.Time.Before(since) {
			out = append(out, bucket)
		}
	}
	return out
}

func minuteBucketsAsPoints(buckets []minuteBucket) []ConnectionPoint {
	out := make([]ConnectionPoint, 0, len(buckets))
	for _, bucket := range buckets {
		out = append(out, ConnectionPoint{
			Time:         bucket.Time,
			Reachable:    bucket.Reachable,
			DownlinkMbps: bucket.DownlinkMbps,
			UplinkMbps:   bucket.UplinkMbps,
			PingMS:       bucket.PingMS,
		})
	}
	return out
}

func aggregateHourly(points []ConnectionPoint) []ConnectionPoint {
	if len(points) == 0 {
		return nil
	}
	out := make([]ConnectionPoint, 0, len(points)/60+1)
	var currentHour time.Time
	var bucket ConnectionPoint
	var samples int
	flush := func() {
		if samples == 0 {
			return
		}
		weight := float64(samples)
		bucket.DownlinkMbps /= weight
		bucket.UplinkMbps /= weight
		bucket.PingMS /= weight
		out = append(out, bucket)
		samples = 0
	}
	for _, point := range points {
		hour := point.Time.Truncate(time.Hour)
		if samples == 0 {
			currentHour = hour
			bucket = ConnectionPoint{Time: hour, Reachable: point.Reachable}
		}
		if !hour.Equal(currentHour) {
			flush()
			currentHour = hour
			bucket = ConnectionPoint{Time: hour, Reachable: point.Reachable}
		}
		samples++
		bucket.Reachable = bucket.Reachable || point.Reachable
		bucket.DownlinkMbps += point.DownlinkMbps
		bucket.UplinkMbps += point.UplinkMbps
		bucket.PingMS += point.PingMS
	}
	flush()
	return out
}

func copyHistory(points []ConnectionPoint) []ConnectionPoint {
	return append([]ConnectionPoint(nil), points...)
}
