package collector

// This file maps StatLite Metrics v1 responses into normalized StatLite samples.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

type StatliteMetricsCollector struct {
	targetName string
	client     *StatliteMetricsClient
}

type statliteMetricsMapping struct {
	sourceKey      string
	normalizedKey  string
	kind           MetricKind
	unit           string
	rejectNegative bool
	maxValue       *float64
	field          StatliteMetricsField
}

func NewStatliteMetricsCollector(targetName string, client *StatliteMetricsClient) *StatliteMetricsCollector {
	return &StatliteMetricsCollector{
		targetName: targetName,
		client:     client,
	}
}

func (c *StatliteMetricsCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	result := &CollectionResult{
		TargetName:    c.targetName,
		PollStartedAt: time.Now().UTC(),
	}
	defer func() {
		result.PollFinishedAt = time.Now().UTC()
	}()

	if c.client == nil {
		err := fmt.Errorf("statlite metrics client is not configured")
		result.addEvent(EventSeverityError, "collector_not_configured", "", err.Error())
		return result, err
	}

	response, err := c.client.Fetch(ctx)
	if err != nil {
		result.addEvent(EventSeverityError, "metrics_fetch_failed", "", err.Error())
		return result, fmt.Errorf("fetching statlite metrics: %w", err)
	}

	result.HealthStatus = response.Status
	collectStatliteMetricsDatabaseStatus(result, response.DatabaseStatus)
	collectStatliteMetricsStartTime(result, response.StartedAt)
	if response.Metrics != nil {
		collectStatliteMetricsSamples(result, response.Metrics)
	}
	return result, nil
}

func collectStatliteMetricsDatabaseStatus(result *CollectionResult, field StatliteMetricsField) {
	if !field.present() {
		return
	}

	var status string
	if err := json.Unmarshal(field.raw, &status); err != nil || strings.TrimSpace(status) == "" {
		result.addEvent(EventSeverityWarning, "database_status_invalid", "database_status", "statlite metrics database_status must be a non-empty string")
		return
	}
	result.DBHealthStatus = strings.TrimSpace(status)
}

func collectStatliteMetricsStartTime(result *CollectionResult, field StatliteMetricsField) {
	if !field.present() {
		result.addEvent(
			EventSeverityWarning,
			"process_start_time_missing",
			"process_start_time",
			"statlite metrics missing started_at; restart detection may be less reliable",
		)
		return
	}

	var startedAt string
	if err := json.Unmarshal(field.raw, &startedAt); err != nil || strings.TrimSpace(startedAt) == "" {
		addStatliteMetricsStartTimeInvalid(result, "started_at must be an RFC3339 string")
		return
	}
	parsed, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		addStatliteMetricsStartTimeInvalid(result, fmt.Sprintf("started_at must be RFC3339: %v", err))
		return
	}

	parsed = parsed.UTC()
	result.ProcessStartTime = &parsed
	unixSeconds := float64(parsed.Unix()) + float64(parsed.Nanosecond())/1_000_000_000
	result.addSample("process_start_time", MetricKindGauge, unixSeconds, "unix_seconds")
}

func addStatliteMetricsStartTimeInvalid(result *CollectionResult, message string) {
	result.addEvent(
		EventSeverityWarning,
		"process_start_time_invalid",
		"process_start_time",
		fmt.Sprintf("statlite metrics %s; restart detection may be less reliable", message),
	)
}

func collectStatliteMetricsSamples(result *CollectionResult, metrics *StatliteMetricsValues) {
	mappings := []statliteMetricsMapping{
		{
			sourceKey:      "requests_total",
			normalizedKey:  "http_requests_total",
			kind:           MetricKindCounter,
			unit:           "requests",
			rejectNegative: true,
			field:          metrics.RequestsTotal,
		},
		{
			sourceKey:      "responses_404_total",
			normalizedKey:  "http_404_total",
			kind:           MetricKindCounter,
			unit:           "requests",
			rejectNegative: true,
			field:          metrics.Responses404Total,
		},
		{
			sourceKey:      "responses_4xx_total",
			normalizedKey:  "http_4xx_total",
			kind:           MetricKindCounter,
			unit:           "requests",
			rejectNegative: true,
			field:          metrics.Responses4xxTotal,
		},
		{
			sourceKey:      "responses_5xx_total",
			normalizedKey:  "http_5xx_total",
			kind:           MetricKindCounter,
			unit:           "requests",
			rejectNegative: true,
			field:          metrics.Responses5xxTotal,
		},
		{
			sourceKey:      "request_duration_seconds_total",
			normalizedKey:  "http_request_time_total_seconds",
			kind:           MetricKindCounter,
			unit:           "seconds",
			rejectNegative: true,
			field:          metrics.RequestDurationSecondsTotal,
		},
		{
			sourceKey:     "process_cpu_usage",
			normalizedKey: "process_cpu_usage",
			kind:          MetricKindGauge,
			unit:          "cores",
			field:         metrics.ProcessCPUUsage,
		},
		{
			sourceKey:      "runtime_heap_used_bytes",
			normalizedKey:  "runtime_heap_used_bytes",
			kind:           MetricKindGauge,
			unit:           "bytes",
			rejectNegative: true,
			field:          metrics.RuntimeHeapUsedBytes,
		},
		{
			sourceKey:     "uptime_seconds",
			normalizedKey: "process_uptime",
			kind:          MetricKindGauge,
			unit:          "seconds",
			field:         metrics.UptimeSeconds,
		},
		{
			sourceKey:      "host_cpu_usage",
			normalizedKey:  "host_cpu_usage",
			kind:           MetricKindGauge,
			unit:           "ratio",
			rejectNegative: true,
			maxValue:       float64Pointer(1),
			field:          metrics.HostCPUUsage,
		},
		{
			sourceKey:      "host_memory_used_bytes",
			normalizedKey:  "host_memory_used_bytes",
			kind:           MetricKindGauge,
			unit:           "bytes",
			rejectNegative: true,
			field:          metrics.HostMemoryUsedBytes,
		},
		{
			sourceKey:      "host_memory_total_bytes",
			normalizedKey:  "host_memory_total_bytes",
			kind:           MetricKindGauge,
			unit:           "bytes",
			rejectNegative: true,
			field:          metrics.HostMemoryTotalBytes,
		},
		{
			sourceKey:      "host_disk_used_bytes",
			normalizedKey:  "host_disk_used_bytes",
			kind:           MetricKindGauge,
			unit:           "bytes",
			rejectNegative: true,
			field:          metrics.HostDiskUsedBytes,
		},
		{
			sourceKey:      "host_disk_total_bytes",
			normalizedKey:  "host_disk_total_bytes",
			kind:           MetricKindGauge,
			unit:           "bytes",
			rejectNegative: true,
			field:          metrics.HostDiskTotalBytes,
		},
	}

	samples := make(map[string]MetricSample, len(mappings))
	for _, mapping := range mappings {
		if sample := collectStatliteMetricsSample(result, mapping); sample != nil {
			samples[mapping.normalizedKey] = *sample
		}
	}
	validateStatliteMetricsPair(result, samples, "host_memory_used_bytes", "host_memory_total_bytes")
	validateStatliteMetricsPair(result, samples, "host_disk_used_bytes", "host_disk_total_bytes")
	for _, mapping := range mappings {
		if sample, ok := samples[mapping.normalizedKey]; ok {
			result.addSample(sample.Key, sample.Kind, sample.Value, sample.Unit)
		}
	}
}

func float64Pointer(value float64) *float64 { return &value }

func collectStatliteMetricsSample(result *CollectionResult, mapping statliteMetricsMapping) *MetricSample {
	if !mapping.field.present() {
		return nil
	}

	var number json.Number
	if err := json.Unmarshal(mapping.field.raw, &number); err != nil {
		addStatliteMetricsValueInvalid(result, mapping, "must be a number")
		return nil
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		addStatliteMetricsValueInvalid(result, mapping, "must be a finite number")
		return nil
	}
	if mapping.rejectNegative && value < 0 {
		addStatliteMetricsValueInvalid(result, mapping, "must be non-negative")
		return nil
	}
	if mapping.maxValue != nil && value > *mapping.maxValue {
		addStatliteMetricsValueInvalid(result, mapping, fmt.Sprintf("must not exceed %g", *mapping.maxValue))
		return nil
	}

	return &MetricSample{Key: mapping.normalizedKey, Kind: mapping.kind, Value: value, Unit: mapping.unit}
}

func validateStatliteMetricsPair(result *CollectionResult, samples map[string]MetricSample, usedKey, totalKey string) {
	used, usedPresent := samples[usedKey]
	total, totalPresent := samples[totalKey]
	if !usedPresent || !totalPresent || used.Value <= total.Value {
		return
	}
	delete(samples, usedKey)
	delete(samples, totalKey)
	for _, sample := range []MetricSample{used, total} {
		result.addEvent(EventSeverityWarning, "metric_invalid", sample.Key, fmt.Sprintf("statlite metrics %s must not exceed %s", usedKey, totalKey))
	}
}

func addStatliteMetricsValueInvalid(result *CollectionResult, mapping statliteMetricsMapping, reason string) {
	result.addEvent(
		EventSeverityWarning,
		"metric_invalid",
		mapping.normalizedKey,
		fmt.Sprintf("statlite metrics %s %s", mapping.sourceKey, reason),
	)
}
