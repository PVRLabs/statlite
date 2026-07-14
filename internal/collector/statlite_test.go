package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStatliteHealthzCollectorCollectsNormalizedSamples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		writeStatliteJSON(t, w, map[string]interface{}{
			"status":    "ok",
			"version":   "0.0.1",
			"timestamp": "2026-07-07T10:00:00Z",
			"statlite": map[string]interface{}{
				"uptime_seconds":     86400,
				"process_start_time": "2026-07-06T10:00:00Z",
				"http": map[string]interface{}{
					"requests_total":     1234,
					"not_found_total":    12,
					"server_error_total": 3,
				},
				"runtime": map[string]interface{}{
					"memory_alloc_bytes": 7340032,
					"memory_sys_bytes":   20971520,
					"goroutines":         18,
					"process_cpu_usage":  0.25,
				},
				"storage": map[string]interface{}{
					"status":              "ok",
					"last_stored_poll_id": 7,
				},
				"polling": map[string]interface{}{
					"consecutive_failures": 0,
				},
			},
		})
	}))
	defer server.Close()

	client, err := NewStatliteHealthzClient(server.URL+"/healthz", time.Second)
	if err != nil {
		t.Fatalf("NewStatliteHealthzClient() error = %v", err)
	}
	collector := NewStatliteHealthzCollector("self", client)

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.TargetName != "self" {
		t.Fatalf("TargetName = %q, want self", result.TargetName)
	}
	if result.HealthStatus != "ok" {
		t.Fatalf("HealthStatus = %q, want ok", result.HealthStatus)
	}
	if result.DBHealthStatus != "ok" {
		t.Fatalf("DBHealthStatus = %q, want ok", result.DBHealthStatus)
	}
	if result.ProcessStartTime == nil || result.ProcessStartTime.Format(time.RFC3339) != "2026-07-06T10:00:00Z" {
		t.Fatalf("ProcessStartTime = %v, want 2026-07-06T10:00:00Z", result.ProcessStartTime)
	}
	assertSample(t, result, "http_requests_total", MetricKindCounter, 1234, "requests")
	assertSample(t, result, "http_404_total", MetricKindCounter, 12, "requests")
	assertSample(t, result, "http_5xx_total", MetricKindCounter, 3, "requests")
	assertSample(t, result, "jvm_heap_used_bytes", MetricKindGauge, 7340032, "bytes")
	assertSample(t, result, "process_uptime", MetricKindGauge, 86400, "seconds")
	assertSample(t, result, "process_start_time", MetricKindGauge, 1783332000, "unix_seconds")
	assertSample(t, result, "process_cpu_usage", MetricKindGauge, 0.25, "ratio")
	assertSample(t, result, "statlite_memory_sys_bytes", MetricKindGauge, 20971520, "bytes")
	assertSample(t, result, "statlite_goroutines", MetricKindGauge, 18, "goroutines")
	if len(result.Events) != 0 {
		t.Fatalf("Events = %#v, want none", result.Events)
	}
}

func TestStatliteHealthzCollectorReportsMissingOptionalMetricsAsWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeStatliteJSON(t, w, map[string]interface{}{
			"status":   "ok",
			"statlite": map[string]interface{}{},
		})
	}))
	defer server.Close()

	client, err := NewStatliteHealthzClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewStatliteHealthzClient() error = %v", err)
	}
	collector := NewStatliteHealthzCollector("self", client)

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(result.Samples) != 0 {
		t.Fatalf("Samples = %#v, want none", result.Samples)
	}
	if countEvents(result, EventSeverityWarning, "metric_missing") != 8 {
		t.Fatalf("Events = %#v, want missing metric warnings", result.Events)
	}
	if countEvents(result, EventSeverityWarning, "db_health_missing") != 1 {
		t.Fatalf("Events = %#v, want missing db health warning", result.Events)
	}
	if countEvents(result, EventSeverityWarning, "process_start_time_missing") != 1 {
		t.Fatalf("Events = %#v, want missing process start warning", result.Events)
	}
}

func TestStatliteHealthzCollectorReturnsPollErrorOnHTTPFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client, err := NewStatliteHealthzClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewStatliteHealthzClient() error = %v", err)
	}
	collector := NewStatliteHealthzCollector("self", client)

	result, err := collector.Collect(context.Background())
	if err == nil {
		t.Fatal("Collect() error = nil, want error")
	}
	if countEvents(result, EventSeverityError, "healthz_fetch_failed") != 1 {
		t.Fatalf("Events = %#v, want healthz_fetch_failed", result.Events)
	}
}

func writeStatliteJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encoding response: %v", err)
	}
}
