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
	return s.series(ctx, s.db, targetName, start, end, false, time.Time{})
}

type seriesQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const PublicMetricsPollLimit = 4096

// BoundedSeries reads at most PublicMetricsPollLimit polls before loading samples.
// Counter baselines before retentionCutoff are ineligible. A read transaction
// keeps the poll selection and counter baselines consistent.
func (s *Store) BoundedSeries(ctx context.Context, targetName string, start, end, retentionCutoff time.Time) (*Series, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin bounded series read: %w", err)
	}
	defer tx.Rollback()
	series, err := s.series(ctx, tx, targetName, start, end, true, retentionCutoff)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit bounded series read: %w", err)
	}
	return series, nil
}

func publicPollIDs(ctx context.Context, db seriesQuerier, targetName string, start, end time.Time) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `
SELECT p.id FROM targets t JOIN polls p ON p.target_id = t.id
WHERE t.name = ? AND p.started_at >= ? AND p.started_at <= ?
ORDER BY p.started_at ASC, p.id ASC LIMIT ?
`, targetName, formatSortableTime(start), formatSortableTime(end), PublicMetricsPollLimit+1)
	if err != nil {
		return nil, fmt.Errorf("query public metric polls: %w", err)
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan public metric poll: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate public metric polls: %w", err)
	}
	if len(ids) > PublicMetricsPollLimit {
		return nil, fmt.Errorf("public metrics poll limit exceeded: more than %d polls in window", PublicMetricsPollLimit)
	}
	return ids, nil
}

func (s *Store) series(ctx context.Context, db seriesQuerier, targetName string, start, end time.Time, bounded bool, retentionCutoff time.Time) (*Series, error) {
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
	previous, err := s.previousCounterValuesFrom(ctx, db, targetName, start, counterKeys, retentionCutoff)
	if err != nil {
		return nil, err
	}
	query := `
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
`
	args := []any{targetName, formatSortableTime(start), formatSortableTime(end)}
	if bounded {
		ids, err := publicPollIDs(ctx, db, targetName, start, end)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return &Series{Start: start.UTC(), End: end.UTC(), Points: []SeriesPoint{}}, nil
		}
		query = strings.Replace(query, "  AND ms.metric_key IN", "  AND p.id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+")\n  AND ms.metric_key IN", 1)
		for _, id := range ids {
			args = append(args, id)
		}
	}
	for _, key := range keys {
		args = append(args, key)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query series samples: %w", err)
	}
	defer rows.Close()

	series := &Series{Start: start.UTC(), End: end.UTC()}
	var current *pollSamples
	flush := func() {
		if current == nil {
			return
		}
		point, include := buildSeriesPoint(current, previous, start)
		if include {
			series.Points = append(series.Points, point)
		}
	}

	for rows.Next() {
		var pollID int64
		var startedAtText, metricKey, metricKind string
		var appRunID sql.NullInt64
		var value float64
		if err := rows.Scan(&pollID, &startedAtText, &appRunID, &metricKey, &metricKind, &value); err != nil {
			return nil, fmt.Errorf("scan series sample: %w", err)
		}
		startedAt, err := parseStoredTime(startedAtText)
		if err != nil {
			return nil, fmt.Errorf("parse series sample started_at: %w", err)
		}
		if current == nil || current.pollID != pollID {
			flush()
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
	flush()
	if len(series.Points) > 0 {
		latest := series.Points[len(series.Points)-1]
		series.LatestPoint = &latest
	}
	setCurrentHostDisk(series)

	return series, nil
}

func (s *Store) previousCounterValuesFrom(ctx context.Context, db seriesQuerier, targetName string, start time.Time, keys []string, retentionCutoff time.Time) (map[string]counterValue, error) {
	previous := make(map[string]counterValue, len(keys))
	for _, key := range keys {
		var pollID int64
		var appRunID sql.NullInt64
		var value float64
		query := `
SELECT p.id, p.app_run_id, ms.value
FROM polls p
JOIN targets t ON t.id = p.target_id
JOIN metric_samples ms ON ms.poll_id = p.id
WHERE t.name = ?
  AND p.started_at < ?
  AND ms.metric_key = ?
  AND ms.metric_kind = ?
`
		args := []any{targetName, formatSortableTime(start), key, collector.MetricKindCounter}
		if !retentionCutoff.IsZero() {
			query += "  AND p.started_at >= ?\n"
			args = append(args, formatSortableTime(retentionCutoff))
		}
		query += `
ORDER BY p.started_at DESC, p.id DESC
LIMIT 1
`
		err := db.QueryRowContext(ctx, query, args...).Scan(&pollID, &appRunID, &value)
		if err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, fmt.Errorf("query previous series sample %q: %w", key, err)
		}
		counter := counterValue{pollID: pollID, value: value}
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
	pollID   int64
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
	point.publicCoverage = publicCounterCoverage{
		paired4xx: requestDelta != nil && point.HTTP4xx != nil && matchingCounterBaseline(previous, "http_requests_total", "http_4xx_total"),
		paired5xx: requestDelta != nil && point.HTTP5xx != nil && matchingCounterBaseline(previous, "http_requests_total", "http_5xx_total"),
	}
	if requestDelta != nil && requestTimeDelta != nil && *requestDelta > 0 &&
		matchingCounterBaseline(previous, "http_requests_total", "http_request_time_total_seconds") {
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

func matchingCounterBaseline(previous map[string]counterValue, firstKey, secondKey string) bool {
	first, firstOK := previous[firstKey]
	second, secondOK := previous[secondKey]
	return firstOK && secondOK && first.pollID == second.pollID
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
		previous[key] = counterValue{pollID: poll.pollID, appRunID: poll.appRunID, value: sample.value}
	}
}

func sameAppRun(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
