package storage

// This file builds dashboard time series and computes counter deltas at query time.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

func (s *Store) Series(ctx context.Context, targetName string, start, end time.Time) (*Series, error) {
	if strings.TrimSpace(targetName) == "" {
		return nil, fmt.Errorf("target name is required")
	}
	if !start.Before(end) {
		return nil, fmt.Errorf("series start must be before end")
	}

	counterKeys := []string{
		"http_requests_total",
		"http_404_total",
		"http_4xx_total",
		"http_5xx_total",
		"http_request_time_total_seconds",
	}
	keys := append(counterKeys, []string{
		"runtime_heap_used_bytes",
		"jvm_heap_used_bytes",
		"process_cpu_usage",
		"host_cpu_usage",
		"host_memory_used_bytes",
		"host_memory_total_bytes",
		"host_disk_used_bytes",
		"host_disk_total_bytes",
	}...)
	previous, err := s.previousCounterValues(ctx, targetName, start, counterKeys)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT
  p.id,
  p.started_at,
  p.app_run_id,
  ms.metric_key,
  ms.metric_kind,
  ms.value
FROM polls p
JOIN targets t ON t.id = p.target_id
JOIN metric_samples ms ON ms.poll_id = p.id
WHERE t.name = ?
  AND p.started_at >= ?
  AND p.started_at <= ?
  AND ms.metric_key IN (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ORDER BY p.started_at ASC, p.id ASC, ms.metric_key ASC
`, targetName, formatTime(start), formatTime(end), keys[0], keys[1], keys[2], keys[3], keys[4], keys[5], keys[6], keys[7], keys[8], keys[9], keys[10], keys[11], keys[12])
	if err != nil {
		return nil, fmt.Errorf("query series samples: %w", err)
	}
	defer rows.Close()

	series := &Series{Start: start.UTC(), End: end.UTC()}
	var current *pollSamples
	flush := func() error {
		if current == nil {
			return nil
		}
		point, include := buildSeriesPoint(current, previous, start)
		if include {
			series.Points = append(series.Points, point)
		}
		return nil
	}

	for rows.Next() {
		var pollID int64
		var startedAtText, metricKey, metricKind string
		var appRunID sql.NullInt64
		var value float64
		if err := rows.Scan(&pollID, &startedAtText, &appRunID, &metricKey, &metricKind, &value); err != nil {
			return nil, fmt.Errorf("scan series sample: %w", err)
		}
		startedAt, err := parseTime(startedAtText)
		if err != nil {
			return nil, fmt.Errorf("parse series sample started_at: %w", err)
		}
		if current == nil || current.pollID != pollID {
			if err := flush(); err != nil {
				return nil, err
			}
			current = &pollSamples{
				pollID:    pollID,
				timestamp: startedAt,
				samples:   make(map[string]sampleValue),
			}
			if appRunID.Valid {
				current.appRunID = &appRunID.Int64
			}
		}
		current.samples[metricKey] = sampleValue{kind: collector.MetricKind(metricKind), value: value}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate series samples: %w", err)
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(series.Points) > 0 {
		latest := series.Points[len(series.Points)-1]
		series.LatestPoint = &latest
	}
	setCurrentHostDisk(series)

	return series, nil
}

func (s *Store) previousCounterValues(ctx context.Context, targetName string, start time.Time, keys []string) (map[string]counterValue, error) {
	previous := make(map[string]counterValue, len(keys))
	for _, key := range keys {
		var appRunID sql.NullInt64
		var value float64
		err := s.db.QueryRowContext(ctx, `
SELECT p.app_run_id, ms.value
FROM polls p
JOIN targets t ON t.id = p.target_id
JOIN metric_samples ms ON ms.poll_id = p.id
WHERE t.name = ?
  AND p.started_at < ?
  AND ms.metric_key = ?
  AND ms.metric_kind = ?
ORDER BY p.started_at DESC, p.id DESC
LIMIT 1
`, targetName, formatTime(start), key, collector.MetricKindCounter).Scan(&appRunID, &value)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, fmt.Errorf("query previous series sample %q: %w", key, err)
		}
		counter := counterValue{value: value}
		if appRunID.Valid {
			id := appRunID.Int64
			counter.appRunID = &id
		}
		previous[key] = counter
	}
	return previous, nil
}

func setCurrentHostDisk(series *Series) {
	if series == nil {
		return
	}
	series.CurrentHostDisk = nil
	if len(series.Points) == 0 {
		return
	}
	point := series.Points[len(series.Points)-1]
	if point.HostDiskUsedBytes == nil || point.HostDiskTotalBytes == nil || point.HostDiskUsage == nil {
		return
	}
	series.CurrentHostDisk = &HostDiskCurrent{
		UsedBytes:  *point.HostDiskUsedBytes,
		TotalBytes: *point.HostDiskTotalBytes,
		Usage:      *point.HostDiskUsage,
	}
}

type pollSamples struct {
	pollID    int64
	timestamp time.Time
	appRunID  *int64
	samples   map[string]sampleValue
}

type sampleValue struct {
	kind  collector.MetricKind
	value float64
}

type counterValue struct {
	appRunID *int64
	value    float64
}

func buildSeriesPoint(poll *pollSamples, previous map[string]counterValue, start time.Time) (SeriesPoint, bool) {
	point := SeriesPoint{
		PollID:    poll.pollID,
		Timestamp: poll.timestamp,
		AppRunID:  poll.appRunID,
	}

	requestDelta := counterDelta(poll, previous, "http_requests_total")
	requestTimeDelta := counterDelta(poll, previous, "http_request_time_total_seconds")
	point.Requests = requestDelta
	point.HTTP404 = counterDelta(poll, previous, "http_404_total")
	point.HTTP4xx = counterDelta(poll, previous, "http_4xx_total")
	point.HTTP5xx = counterDelta(poll, previous, "http_5xx_total")
	if requestDelta != nil && requestTimeDelta != nil && *requestDelta > 0 {
		value := *requestTimeDelta / *requestDelta
		point.AverageLatencySeconds = &value
	}
	point.HeapUsedBytes = runtimeMemoryValue(poll)
	point.ProcessCPUUsage = gaugeValue(poll, "process_cpu_usage")
	point.HostCPUUsage = gaugeValue(poll, "host_cpu_usage")
	point.HostMemoryUsedBytes = gaugeValue(poll, "host_memory_used_bytes")
	point.HostMemoryTotalBytes = gaugeValue(poll, "host_memory_total_bytes")
	point.HostMemoryUsage = usagePercentage(point.HostMemoryUsedBytes, point.HostMemoryTotalBytes)
	point.HostDiskUsedBytes = gaugeValue(poll, "host_disk_used_bytes")
	point.HostDiskTotalBytes = gaugeValue(poll, "host_disk_total_bytes")
	point.HostDiskUsage = usagePercentage(point.HostDiskUsedBytes, point.HostDiskTotalBytes)

	updatePreviousCounters(poll, previous)
	return point, !poll.timestamp.Before(start)
}

func usagePercentage(used, total *float64) *float64 {
	if used == nil || total == nil || *used < 0 || *total <= 0 || *used > *total {
		return nil
	}
	value := *used / *total
	return &value
}

func runtimeMemoryValue(poll *pollSamples) *float64 {
	if value := gaugeValue(poll, "runtime_heap_used_bytes"); value != nil {
		return value
	}
	return gaugeValue(poll, "jvm_heap_used_bytes")
}

func counterDelta(poll *pollSamples, previous map[string]counterValue, key string) *float64 {
	sample, ok := poll.samples[key]
	if !ok || sample.kind != collector.MetricKindCounter {
		return nil
	}
	previousSample, ok := previous[key]
	if !ok || !sameAppRun(previousSample.appRunID, poll.appRunID) {
		return nil
	}
	delta := sample.value - previousSample.value
	if delta < 0 {
		return nil
	}
	return &delta
}

func gaugeValue(poll *pollSamples, key string) *float64 {
	sample, ok := poll.samples[key]
	if !ok || sample.kind != collector.MetricKindGauge {
		return nil
	}
	value := sample.value
	return &value
}

func updatePreviousCounters(poll *pollSamples, previous map[string]counterValue) {
	for key, sample := range poll.samples {
		if sample.kind != collector.MetricKindCounter {
			continue
		}
		previous[key] = counterValue{appRunID: poll.appRunID, value: sample.value}
	}
}

func sameAppRun(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
