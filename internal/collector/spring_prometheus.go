package collector

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

type springPrometheusValues struct {
	supportedNonCounterMetric bool
	processStart              bool
	sawHTTPCount              bool
	completeHTTPStatusLabels  bool
	values                    map[string]float64
	invalidCounters           map[string]bool
	counterOverflows          map[string]bool
}

func (c *SpringActuatorCollector) collectMetrics(ctx context.Context, session *springPollSession, result *CollectionResult) bool {
	source := c.selectedSource
	if source == SpringMetricsSourceActuator {
		c.collectActuatorMetrics(ctx, session, result)
		return true
	}
	values, err := c.scrapePrometheus(ctx)
	if source == SpringMetricsSourcePrometheus {
		if err != nil {
			result.addEvent(EventSeverityWarning, "metrics_fetch_failed", "", fmt.Sprintf("collecting Spring Prometheus metrics: %v", err))
			return false
		}
		if !values.compatible() {
			result.addEvent(EventSeverityWarning, "metrics_source_incompatible", "", "Spring Prometheus endpoint does not expose the required supported metric families")
			return false
		}
		c.addPrometheusSamples(result, values)
		return true
	}

	if err == nil && values.compatible() {
		c.selectedSource = SpringMetricsSourcePrometheus
		c.addPrometheusSamples(result, values)
		return true
	}
	if err == nil || prometheusDefinitelyAbsent(err) {
		c.selectedSource = SpringMetricsSourceActuator
		c.collectActuatorMetrics(ctx, session, result)
		return true
	}
	result.addEvent(EventSeverityWarning, "metrics_source_unresolved", "", fmt.Sprintf("Spring metrics source remains unresolved: %v", err))
	return false
}

func (c *SpringActuatorCollector) scrapePrometheus(ctx context.Context) (*springPrometheusValues, error) {
	if c.prometheusClient == nil || c.prometheusURL == "" {
		return nil, errors.New("Prometheus client is not configured")
	}
	v := &springPrometheusValues{values: make(map[string]float64)}
	_, err := c.prometheusClient.Scrape(ctx, c.prometheusURL, func(sample prometheus.Sample) error {
		return v.accept(sample, c.collectHostMetrics)
	})
	return v, err
}

func (v *springPrometheusValues) compatible() bool {
	if !v.processStart {
		return false
	}
	if v.supportedNonCounterMetric {
		return true
	}
	for _, key := range []string{"http_requests_total", "http_404_total", "http_4xx_total", "http_5xx_total", "http_request_time_total_seconds"} {
		if value, ok := v.values[key]; ok && !v.invalidCounters[key] && finiteNonnegative(value) {
			return true
		}
	}
	return false
}

func (v *springPrometheusValues) accept(sample prometheus.Sample, host bool) error {
	labels := make(map[string]string, len(sample.Labels))
	for _, label := range sample.Labels {
		labels[label.Name] = label.Value
	}
	switch sample.Name {
	case "http_server_requests_seconds_count":
		if !v.sawHTTPCount {
			v.completeHTTPStatusLabels = true
		}
		v.sawHTTPCount = true
		v.addCounter("http_requests_total", sample.Value)
		status, hasStatus := labels["status"]
		if !hasStatus {
			v.completeHTTPStatusLabels = false
		}
		if code, err := strconv.Atoi(status); hasStatus && err == nil {
			if code == 404 {
				v.addCounter("http_404_total", sample.Value)
			}
			if code >= 400 && code <= 499 {
				v.addCounter("http_4xx_total", sample.Value)
			}
			if code >= 500 && code <= 599 {
				v.addCounter("http_5xx_total", sample.Value)
			}
		}
	case "http_server_requests_seconds_sum":
		v.addCounter("http_request_time_total_seconds", sample.Value)
	default:
		if math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) {
			return nil
		}
		switch sample.Name {
		case "jvm_memory_used_bytes":
			if labels["area"] == "heap" {
				v.supportedNonCounterMetric = true
				v.values["jvm_heap_used_bytes"] += sample.Value
			}
		case "process_cpu_usage":
			v.supportedNonCounterMetric = true
			v.values["process_cpu_usage"] = sample.Value
		case "process_start_time_seconds":
			v.processStart = true
			v.values["process_start_time"] = sample.Value
		case "system_cpu_usage":
			if host {
				v.supportedNonCounterMetric = true
				v.values["host_cpu_usage"] = sample.Value
			}
		case "disk_free_bytes", "disk_total_bytes":
			if host {
				v.supportedNonCounterMetric = true
				v.values[sample.Name] += sample.Value
			}
		}
	}
	return nil
}

func (v *springPrometheusValues) addCounter(key string, value float64) {
	if v.values == nil {
		v.values = make(map[string]float64)
	}
	if v.invalidCounters == nil {
		v.invalidCounters = make(map[string]bool)
	}
	if v.counterOverflows == nil {
		v.counterOverflows = make(map[string]bool)
	}
	if !finiteNonnegative(value) {
		v.invalidCounters[key] = true
		return
	}
	if v.invalidCounters[key] {
		return
	}
	total, ok := addFiniteNonnegative(v.values[key], value)
	if !ok {
		v.invalidCounters[key] = true
		v.counterOverflows[key] = true
		return
	}
	v.values[key] = total
}

func (c *SpringActuatorCollector) addPrometheusSamples(result *CollectionResult, v *springPrometheusValues) {
	addCounter := func(key, unit string, emitZero bool) {
		value, ok := v.values[key]
		if !ok && !emitZero {
			return
		}
		if v.invalidCounters[key] {
			if v.counterOverflows[key] {
				result.addEvent(EventSeverityWarning, "metric_aggregate_invalid", key, fmt.Sprintf("omitted %s because finite source values overflowed the normalized aggregate", key))
			} else {
				result.addEvent(EventSeverityWarning, "metric_invalid", key, fmt.Sprintf("omitted %s because source values must be finite and non-negative", key))
			}
			return
		}
		if !finiteNonnegative(value) {
			result.addEvent(EventSeverityWarning, "metric_invalid", key, fmt.Sprintf("omitted %s because the normalized value is not finite and non-negative", key))
			return
		}
		result.addSample(key, MetricKindCounter, value, unit)
	}
	add := func(key string, kind MetricKind, unit string) {
		if kind == MetricKindCounter {
			addCounter(key, unit, false)
			return
		}
		if value, ok := v.values[key]; ok {
			result.addSample(key, kind, value, unit)
		}
	}
	add("http_requests_total", MetricKindCounter, "requests")
	if v.sawHTTPCount && v.completeHTTPStatusLabels {
		for _, key := range []string{"http_404_total", "http_4xx_total", "http_5xx_total"} {
			addCounter(key, "requests", true)
		}
	} else if v.sawHTTPCount {
		result.addEvent(EventSeverityWarning, "metric_tag_missing", "http_4xx_total", "http_server_requests_seconds_count does not consistently expose status labels")
	}
	add("http_request_time_total_seconds", MetricKindCounter, "seconds")
	add("jvm_heap_used_bytes", MetricKindGauge, "bytes")
	add("process_cpu_usage", MetricKindGauge, "ratio")
	if value, ok := v.values["host_cpu_usage"]; ok {
		if finiteInRange(value, 0, 1) {
			result.addSample("host_cpu_usage", MetricKindGauge, value, "ratio")
		} else {
			result.addEvent(EventSeverityWarning, "metric_invalid", "host_cpu_usage", fmt.Sprintf("system_cpu_usage value %v must be between 0 and 1", value))
		}
	}
	if free, fok := v.values["disk_free_bytes"]; fok {
		if total, tok := v.values["disk_total_bytes"]; tok && total > 0 && free >= 0 && free <= total {
			result.addSample("host_disk_used_bytes", MetricKindGauge, total-free, "bytes")
			result.addSample("host_disk_total_bytes", MetricKindGauge, total, "bytes")
		}
	}
	if value, ok := v.values["process_start_time"]; ok {
		result.addSample("process_start_time", MetricKindGauge, value, "unix_seconds")
		started := unixSeconds(value)
		result.ProcessStartTime = &started
	}
}

func prometheusDefinitelyAbsent(err error) bool {
	var scrapeErr *prometheus.Error
	return errors.As(err, &scrapeErr) && scrapeErr.Class == prometheus.FailureNotFound
}
