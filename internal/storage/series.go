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

	keys := []string{
		"http_requests_total",
		"http_404_total",
		"http_4xx_total",
		"http_5xx_total",
		"http_request_time_total_seconds",
		"jvm_heap_used_bytes",
		"process_cpu_usage",
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
  AND p.started_at <= ?
  AND ms.metric_key IN (?, ?, ?, ?, ?, ?, ?)
ORDER BY p.started_at ASC, p.id ASC, ms.metric_key ASC
`, targetName, formatTime(end), keys[0], keys[1], keys[2], keys[3], keys[4], keys[5], keys[6])
	if err != nil {
		return nil, fmt.Errorf("query series samples: %w", err)
	}
	defer rows.Close()

	series := &Series{Start: start.UTC(), End: end.UTC()}
	previous := make(map[string]counterValue)
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

	return series, nil
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
	point.HeapUsedBytes = gaugeValue(poll, "jvm_heap_used_bytes")
	point.ProcessCPUUsage = gaugeValue(poll, "process_cpu_usage")

	updatePreviousCounters(poll, previous)
	return point, !poll.timestamp.Before(start)
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
