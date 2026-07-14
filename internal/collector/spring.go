package collector

// This file maps Spring Boot Actuator responses into normalized StatLite samples.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type SpringActuatorCollector struct {
	targetName string
	client     *ActuatorClient
}

func NewSpringActuatorCollector(targetName string, client *ActuatorClient) *SpringActuatorCollector {
	return &SpringActuatorCollector{
		targetName: targetName,
		client:     client,
	}
}

func (c *SpringActuatorCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	started := time.Now().UTC()
	result := &CollectionResult{
		TargetName:    c.targetName,
		PollStartedAt: started,
	}
	defer func() {
		result.PollFinishedAt = time.Now().UTC()
	}()

	if c.client == nil {
		err := fmt.Errorf("actuator client is not configured")
		result.addEvent(EventSeverityError, "collector_not_configured", "", err.Error())
		return result, err
	}

	health, err := c.client.FetchHealth(ctx)
	if err != nil {
		result.addEvent(EventSeverityError, "health_fetch_failed", "", err.Error())
		return result, fmt.Errorf("fetching health: %w", err)
	}
	result.HealthStatus = health.Status
	result.DBHealthStatus = health.DBStatus()

	c.collectHTTP(ctx, result)
	c.collectGauge(ctx, result, "jvm_heap_used_bytes", "jvm.memory.used", []string{"area:heap"}, "VALUE", "bytes")
	c.collectGauge(ctx, result, "jvm_heap_max_bytes", "jvm.memory.max", []string{"area:heap"}, "VALUE", "bytes")
	c.collectGauge(ctx, result, "process_cpu_usage", "process.cpu.usage", nil, "VALUE", "ratio")
	c.collectProcessStartTime(ctx, result)

	return result, nil
}

func (c *SpringActuatorCollector) collectHTTP(ctx context.Context, result *CollectionResult) {
	metric, err := c.client.FetchMetric(ctx, "http.server.requests", nil)
	if err != nil {
		result.addEvent(EventSeverityWarning, "metric_fetch_failed", "http_requests_total", err.Error())
		return
	}

	if value, ok := metricMeasurement(metric, "COUNT"); ok {
		result.addSample("http_requests_total", MetricKindCounter, value, "requests")
	} else {
		result.addEvent(EventSeverityWarning, "metric_measurement_missing", "http_requests_total", "http.server.requests missing COUNT measurement")
	}
	if value, ok := metricMeasurement(metric, "TOTAL_TIME"); ok {
		result.addSample("http_request_time_total_seconds", MetricKindCounter, value, "seconds")
	} else {
		result.addEvent(EventSeverityWarning, "metric_measurement_missing", "http_request_time_total_seconds", "http.server.requests missing TOTAL_TIME measurement")
	}
	if value, ok := metricMeasurement(metric, "MAX"); ok {
		result.addSample("http_request_time_max_seconds", MetricKindGauge, value, "seconds")
	}

	statuses := metricTagValues(metric, "status")
	if len(statuses) == 0 {
		result.addEvent(EventSeverityWarning, "metric_tag_missing", "http_4xx_total", "http.server.requests does not expose status tags")
		return
	}

	c.collectHTTPStatusTotal(ctx, result, "http_404_total", filterStatusExact(statuses, "404"))
	c.collectHTTPStatusTotal(ctx, result, "http_4xx_total", filterStatusRange(statuses, 400, 499))
	c.collectHTTPStatusTotal(ctx, result, "http_5xx_total", filterStatusRange(statuses, 500, 599))
}

func (c *SpringActuatorCollector) collectHTTPStatusTotal(ctx context.Context, result *CollectionResult, key string, statuses []string) {
	var total float64
	var sawStatus bool

	for _, status := range statuses {
		metric, err := c.client.FetchMetric(ctx, "http.server.requests", []string{"status:" + status})
		if err != nil {
			result.addEvent(EventSeverityWarning, "metric_fetch_failed", key, fmt.Sprintf("http.server.requests status %s: %v", status, err))
			continue
		}
		value, ok := metricMeasurement(metric, "COUNT")
		if !ok {
			result.addEvent(EventSeverityWarning, "metric_measurement_missing", key, fmt.Sprintf("http.server.requests status %s missing COUNT measurement", status))
			continue
		}
		total += value
		sawStatus = true
	}

	if sawStatus {
		result.addSample(key, MetricKindCounter, total, "requests")
		return
	}
	if len(statuses) == 0 {
		result.addSample(key, MetricKindCounter, 0, "requests")
	}
}

func (c *SpringActuatorCollector) collectGauge(ctx context.Context, result *CollectionResult, key, actuatorName string, tags []string, statistic, unit string) {
	metric, err := c.client.FetchMetric(ctx, actuatorName, tags)
	if err != nil {
		result.addEvent(EventSeverityWarning, "metric_fetch_failed", key, err.Error())
		return
	}

	value, ok := metricMeasurement(metric, statistic)
	if !ok {
		result.addEvent(EventSeverityWarning, "metric_measurement_missing", key, fmt.Sprintf("%s missing %s measurement", actuatorName, statistic))
		return
	}
	result.addSample(key, MetricKindGauge, value, unit)
}

func (c *SpringActuatorCollector) collectProcessStartTime(ctx context.Context, result *CollectionResult) {
	metric, err := c.client.FetchMetric(ctx, "process.start.time", nil)
	if err != nil {
		result.addEvent(EventSeverityWarning, "metric_fetch_failed", "process_start_time", err.Error())
		return
	}

	value, ok := metricMeasurement(metric, "VALUE")
	if !ok {
		result.addEvent(EventSeverityWarning, "metric_measurement_missing", "process_start_time", "process.start.time missing VALUE measurement")
		return
	}

	result.addSample("process_start_time", MetricKindGauge, value, "unix_seconds")
	startTime := unixSeconds(value)
	result.ProcessStartTime = &startTime
}

func metricMeasurement(metric *MetricResponse, statistic string) (float64, bool) {
	for _, measurement := range metric.Measurements {
		if strings.EqualFold(measurement.Statistic, statistic) {
			return measurement.Value, true
		}
	}
	return 0, false
}

func metricTagValues(metric *MetricResponse, tag string) []string {
	for _, availableTag := range metric.AvailableTags {
		if strings.EqualFold(availableTag.Tag, tag) {
			return availableTag.Values
		}
	}
	return nil
}

func filterStatusRange(statuses []string, min, max int) []string {
	var filtered []string
	for _, status := range statuses {
		code, err := strconv.Atoi(status)
		if err != nil {
			continue
		}
		if code >= min && code <= max {
			filtered = append(filtered, status)
		}
	}
	return filtered
}

func filterStatusExact(statuses []string, exact string) []string {
	for _, status := range statuses {
		if status == exact {
			return []string{status}
		}
	}
	return nil
}

func unixSeconds(value float64) time.Time {
	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * 1_000_000_000)
	return time.Unix(seconds, nanos).UTC()
}
