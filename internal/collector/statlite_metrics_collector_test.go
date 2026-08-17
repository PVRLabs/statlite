package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStatliteMetricsCollectorMapsCompleteResponse(t *testing.T) {
	server := newStatliteMetricsServer(t, http.StatusOK, `{
		"schema": "statlite-metrics/v1",
		"status": "UP",
		"database_status": "UP",
		"started_at": "2026-07-27T12:00:00-07:00",
		"target_name": "remote-name-must-be-ignored",
		"poll_started_at": "2000-01-01T00:00:00Z",
		"metrics": {
			"requests_total": 1420,
			"responses_404_total": 18,
			"responses_4xx_total": 31,
			"responses_5xx_total": 4,
			"request_duration_seconds_total": 84.31,
			"request_duration_seconds_max": 1.42,
			"process_cpu_usage": 0.031,
			"host_cpu_usage": 0.4,
			"host_memory_used_bytes": 30,
			"host_memory_total_bytes": 100,
			"host_disk_used_bytes": 40,
			"host_disk_total_bytes": 200,
			"runtime_heap_used_bytes": 25165824,
			"uptime_seconds": 1820
		}
	}`)
	defer server.Close()

	before := time.Now().UTC()
	collector := NewStatliteMetricsCollector("configured-name", newTestStatliteMetricsClient(t, server.URL))
	result, err := collector.Collect(context.Background())
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if result.TargetName != "configured-name" {
		t.Fatalf("TargetName = %q, want configured-name", result.TargetName)
	}
	if result.HealthStatus != "UP" {
		t.Fatalf("HealthStatus = %q, want UP", result.HealthStatus)
	}
	if result.DBHealthStatus != "UP" {
		t.Fatalf("DBHealthStatus = %q, want UP", result.DBHealthStatus)
	}
	if result.PollStartedAt.Before(before) || result.PollStartedAt.After(after) {
		t.Fatalf("PollStartedAt = %v, want local time between %v and %v", result.PollStartedAt, before, after)
	}
	if result.PollFinishedAt.Before(result.PollStartedAt) || result.PollFinishedAt.After(after) {
		t.Fatalf("PollFinishedAt = %v, want between start and %v", result.PollFinishedAt, after)
	}
	if result.ProcessStartTime == nil || result.ProcessStartTime.Format(time.RFC3339) != "2026-07-27T19:00:00Z" {
		t.Fatalf("ProcessStartTime = %v, want 2026-07-27T19:00:00Z", result.ProcessStartTime)
	}

	assertSample(t, result, "http_requests_total", MetricKindCounter, 1420, "requests")
	assertSample(t, result, "http_404_total", MetricKindCounter, 18, "requests")
	assertSample(t, result, "http_4xx_total", MetricKindCounter, 31, "requests")
	assertSample(t, result, "http_5xx_total", MetricKindCounter, 4, "requests")
	assertSample(t, result, "http_request_time_total_seconds", MetricKindCounter, 84.31, "seconds")
	assertSample(t, result, "process_cpu_usage", MetricKindGauge, 0.031, "cores")
	assertSample(t, result, "host_cpu_usage", MetricKindGauge, 0.4, "ratio")
	assertSample(t, result, "host_memory_used_bytes", MetricKindGauge, 30, "bytes")
	assertSample(t, result, "host_memory_total_bytes", MetricKindGauge, 100, "bytes")
	assertSample(t, result, "host_disk_used_bytes", MetricKindGauge, 40, "bytes")
	assertSample(t, result, "host_disk_total_bytes", MetricKindGauge, 200, "bytes")
	assertSample(t, result, "runtime_heap_used_bytes", MetricKindGauge, 25165824, "bytes")
	assertSample(t, result, "process_uptime", MetricKindGauge, 1820, "seconds")
	assertSample(t, result, "process_start_time", MetricKindGauge, 1785178800, "unix_seconds")
	assertSampleKeys(t, result, []string{
		"http_404_total",
		"http_4xx_total",
		"http_5xx_total",
		"http_request_time_total_seconds",
		"http_requests_total",
		"host_cpu_usage",
		"host_disk_total_bytes",
		"host_disk_used_bytes",
		"host_memory_total_bytes",
		"host_memory_used_bytes",
		"process_cpu_usage",
		"process_start_time",
		"process_uptime",
		"runtime_heap_used_bytes",
	})
	if len(result.Events) != 0 {
		t.Fatalf("Events = %#v, want none", result.Events)
	}
}

func TestStatliteMetricsCollectorAllowsMinimalResponse(t *testing.T) {
	server := newStatliteMetricsServer(t, http.StatusOK, `{
		"schema": "statlite-metrics/v1",
		"status": "DEGRADED"
	}`)
	defer server.Close()

	collector := NewStatliteMetricsCollector("minimal", newTestStatliteMetricsClient(t, server.URL))
	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.HealthStatus != "DEGRADED" {
		t.Fatalf("HealthStatus = %q, want DEGRADED", result.HealthStatus)
	}
	if len(result.Samples) != 0 {
		t.Fatalf("Samples = %#v, want none", result.Samples)
	}
	if countEvents(result, EventSeverityWarning, "process_start_time_missing") != 1 {
		t.Fatalf("Events = %#v, want one missing start warning", result.Events)
	}
	if countEvents(result, EventSeverityWarning, "metric_missing") != 0 {
		t.Fatalf("Events = %#v, want no missing metric warnings", result.Events)
	}
}

func TestStatliteMetricsCollectorDoesNotWarnForMissingOptionalMetrics(t *testing.T) {
	server := newStatliteMetricsServer(t, http.StatusOK, `{
		"schema": "statlite-metrics/v1",
		"status": "UP",
		"started_at": "2026-07-27T19:00:00Z",
		"metrics": {
			"requests_total": 12
		}
	}`)
	defer server.Close()

	collector := NewStatliteMetricsCollector("sparse", newTestStatliteMetricsClient(t, server.URL))
	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	assertSample(t, result, "http_requests_total", MetricKindCounter, 12, "requests")
	if len(result.Events) != 0 {
		t.Fatalf("Events = %#v, want no warnings for missing optional metrics", result.Events)
	}
}

func TestStatliteMetricsCollectorWarnsForInvalidDatabaseStatus(t *testing.T) {
	server := newStatliteMetricsServer(t, http.StatusOK, `{
		"schema": "statlite-metrics/v1",
		"status": "UP",
		"database_status": 42,
		"started_at": "2026-07-27T19:00:00Z"
	}`)
	defer server.Close()

	result, err := NewStatliteMetricsCollector("invalid-db", newTestStatliteMetricsClient(t, server.URL)).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.DBHealthStatus != "" {
		t.Fatalf("DBHealthStatus = %q, want empty", result.DBHealthStatus)
	}
	if !hasCollectorEvent(result, "database_status_invalid", "database_status") {
		t.Fatalf("Events = %#v, want invalid database status warning", result.Events)
	}
}

func TestStatliteMetricsCollectorAcceptsFastAPIApplicationProcessProfile(t *testing.T) {
	server := newStatliteMetricsServer(t, http.StatusOK, `{
		"schema": "statlite-metrics/v1",
		"status": "UP",
		"started_at": "2026-07-29T12:00:00Z",
		"metrics": {
			"requests_total": 12,
			"responses_404_total": 2,
			"responses_4xx_total": 3,
			"responses_5xx_total": 1,
			"request_duration_seconds_total": 1.25,
			"request_duration_seconds_max": 0.5,
			"process_cpu_usage": 1.25,
			"runtime_heap_used_bytes": 1024,
			"uptime_seconds": 60
		}
	}`)
	defer server.Close()

	result, err := NewStatliteMetricsCollector("fastapi", newTestStatliteMetricsClient(t, server.URL)).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	assertSample(t, result, "process_cpu_usage", MetricKindGauge, 1.25, "cores")
	assertSample(t, result, "runtime_heap_used_bytes", MetricKindGauge, 1024, "bytes")
	for _, key := range []string{"host_cpu_usage", "host_memory_used_bytes", "host_memory_total_bytes", "host_disk_used_bytes", "host_disk_total_bytes"} {
		if hasSample(result, key) {
			t.Errorf("unexpected host sample %q in application/process-only FastAPI response", key)
		}
	}
	if len(result.Events) != 0 {
		t.Fatalf("Events = %#v, want no warnings for omitted optional host fields", result.Events)
	}
}

func TestStatliteMetricsCollectorWarnsForInvalidStartedAt(t *testing.T) {
	tests := []struct {
		name      string
		startedAt string
	}{
		{name: "not RFC3339", startedAt: `"July 27, 2026"`},
		{name: "wrong type", startedAt: `123`},
		{name: "empty", startedAt: `""`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{
				"schema": "statlite-metrics/v1",
				"status": "UP",
				"started_at": ` + tt.startedAt + `,
				"metrics": {"requests_total": 12}
			}`
			server := newStatliteMetricsServer(t, http.StatusOK, body)
			defer server.Close()

			collector := NewStatliteMetricsCollector("invalid-start", newTestStatliteMetricsClient(t, server.URL))
			result, err := collector.Collect(context.Background())
			if err != nil {
				t.Fatalf("Collect() error = %v", err)
			}
			assertSample(t, result, "http_requests_total", MetricKindCounter, 12, "requests")
			if result.ProcessStartTime != nil {
				t.Fatalf("ProcessStartTime = %v, want nil", result.ProcessStartTime)
			}
			if countEvents(result, EventSeverityWarning, "process_start_time_invalid") != 1 {
				t.Fatalf("Events = %#v, want invalid start warning", result.Events)
			}
			if !strings.Contains(result.Events[0].Message, "restart detection may be less reliable") {
				t.Fatalf("warning = %q, want restart reliability context", result.Events[0].Message)
			}
		})
	}
}

func TestStatliteMetricsCollectorKeepsValidSamplesWhenOptionalMetricsAreInvalid(t *testing.T) {
	server := newStatliteMetricsServer(t, http.StatusOK, `{
		"schema": "statlite-metrics/v1",
		"status": "UP",
		"started_at": "2026-07-27T19:00:00Z",
		"metrics": {
			"requests_total": -1,
			"responses_404_total": 18,
			"responses_4xx_total": 31,
			"responses_5xx_total": "invalid",
			"request_duration_seconds_total": 1e999,
			"request_duration_seconds_max": 1.42,
			"process_cpu_usage": 1e999,
			"runtime_heap_used_bytes": -5,
			"uptime_seconds": 1820
		}
	}`)
	defer server.Close()

	collector := NewStatliteMetricsCollector("mixed", newTestStatliteMetricsClient(t, server.URL))
	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	assertSampleKeys(t, result, []string{
		"http_404_total",
		"http_4xx_total",
		"process_start_time",
		"process_uptime",
	})
	if countEvents(result, EventSeverityWarning, "metric_invalid") != 5 {
		t.Fatalf("Events = %#v, want five invalid metric warnings", result.Events)
	}
	for _, key := range []string{
		"http_requests_total",
		"http_5xx_total",
		"http_request_time_total_seconds",
		"process_cpu_usage",
		"runtime_heap_used_bytes",
	} {
		if !hasCollectorEvent(result, "metric_invalid", key) {
			t.Errorf("Events = %#v, want metric_invalid for %s", result.Events, key)
		}
	}
}

func TestStatliteMetricsCollectorAllowsCPUAboveOneAnd404Subset(t *testing.T) {
	server := newStatliteMetricsServer(t, http.StatusOK, `{
		"schema": "statlite-metrics/v1",
		"status": "UP",
		"started_at": "2026-07-27T19:00:00Z",
		"metrics": {
			"responses_404_total": 18,
			"responses_4xx_total": 31,
			"process_cpu_usage": 2.75
		}
	}`)
	defer server.Close()

	collector := NewStatliteMetricsCollector("valid-edge", newTestStatliteMetricsClient(t, server.URL))
	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	assertSample(t, result, "http_404_total", MetricKindCounter, 18, "requests")
	assertSample(t, result, "http_4xx_total", MetricKindCounter, 31, "requests")
	assertSample(t, result, "process_cpu_usage", MetricKindGauge, 2.75, "cores")
	if len(result.Events) != 0 {
		t.Fatalf("Events = %#v, want none", result.Events)
	}
}

func TestStatliteMetricsCollectorRejectsInvalidHostPairsWithoutDiscardingOtherSamples(t *testing.T) {
	server := newStatliteMetricsServer(t, http.StatusOK, `{
		"schema": "statlite-metrics/v1",
		"status": "UP",
		"started_at": "2026-07-27T19:00:00Z",
		"metrics": {
			"process_cpu_usage": 2.75,
			"host_cpu_usage": 1.2,
			"host_memory_used_bytes": 101,
			"host_memory_total_bytes": 100,
			"host_disk_used_bytes": 101,
			"host_disk_total_bytes": 100,
			"runtime_heap_used_bytes": 10
		}
	}`)
	defer server.Close()

	result, err := NewStatliteMetricsCollector("invalid-host", newTestStatliteMetricsClient(t, server.URL)).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	assertSample(t, result, "process_cpu_usage", MetricKindGauge, 2.75, "cores")
	assertSample(t, result, "runtime_heap_used_bytes", MetricKindGauge, 10, "bytes")
	for _, key := range []string{"host_cpu_usage", "host_memory_used_bytes", "host_memory_total_bytes", "host_disk_used_bytes", "host_disk_total_bytes"} {
		if hasSample(result, key) {
			t.Errorf("Samples = %#v, want %s omitted", result.Samples, key)
		}
		if !hasCollectorEvent(result, "metric_invalid", key) {
			t.Errorf("Events = %#v, want metric_invalid for %s", result.Events, key)
		}
	}
}

func TestStatliteMetricsCollectorRejectsNegativeHostCPU(t *testing.T) {
	server := newStatliteMetricsServer(t, http.StatusOK, `{
		"schema": "statlite-metrics/v1",
		"status": "UP",
		"metrics": {"host_cpu_usage": -0.01}
	}`)
	defer server.Close()
	result, err := NewStatliteMetricsCollector("negative-host-cpu", newTestStatliteMetricsClient(t, server.URL)).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if hasSample(result, "host_cpu_usage") || !hasCollectorEvent(result, "metric_invalid", "host_cpu_usage") {
		t.Fatalf("result = %#v, want rejected negative host CPU", result)
	}
}

func TestStatliteMetricsCollectorReturnsPollErrorWhenFetchFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	collector := NewStatliteMetricsCollector("failed", newTestStatliteMetricsClient(t, server.URL))
	result, err := collector.Collect(context.Background())
	if err == nil {
		t.Fatal("Collect() error = nil, want fetch error")
	}
	if countEvents(result, EventSeverityError, "metrics_fetch_failed") != 1 {
		t.Fatalf("Events = %#v, want metrics_fetch_failed", result.Events)
	}
	if result.TargetName != "failed" || result.PollStartedAt.IsZero() || result.PollFinishedAt.IsZero() {
		t.Fatalf("Result = %#v, want local target and poll timestamps", result)
	}
}

func TestStatliteMetricsCollectorRejectsNilClient(t *testing.T) {
	collector := NewStatliteMetricsCollector("missing-client", nil)
	result, err := collector.Collect(context.Background())
	if err == nil {
		t.Fatal("Collect() error = nil, want configuration error")
	}
	if countEvents(result, EventSeverityError, "collector_not_configured") != 1 {
		t.Fatalf("Events = %#v, want collector_not_configured", result.Events)
	}
}

func hasCollectorEvent(result *CollectionResult, eventType, metricKey string) bool {
	for _, event := range result.Events {
		if event.Type == eventType && event.MetricKey == metricKey {
			return true
		}
	}
	return false
}

func hasSample(result *CollectionResult, key string) bool {
	for _, sample := range result.Samples {
		if sample.Key == key {
			return true
		}
	}
	return false
}
