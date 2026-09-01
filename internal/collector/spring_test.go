package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSpringActuatorCollectorCollectsNormalizedBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/actuator/health":
			writeActuatorJSON(t, w, map[string]interface{}{
				"status": "UP",
				"components": map[string]interface{}{
					"db": map[string]string{"status": "UP"},
				},
			})
		case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "":
			writeActuatorJSON(t, w, metricBody("http.server.requests", "seconds", map[string]float64{
				"COUNT":      20,
				"TOTAL_TIME": 4.2,
				"MAX":        0.9,
			}, map[string][]string{"status": {"200", "404", "500"}}))
		case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "status:404":
			writeActuatorJSON(t, w, metricBody("http.server.requests", "seconds", map[string]float64{"COUNT": 3}, nil))
		case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "status:500":
			writeActuatorJSON(t, w, metricBody("http.server.requests", "seconds", map[string]float64{"COUNT": 2}, nil))
		case r.URL.Path == "/actuator/metrics/jvm.memory.used":
			if r.URL.Query().Get("tag") != "area:heap" {
				t.Fatalf("jvm.memory.used tag = %q, want area:heap", r.URL.Query().Get("tag"))
			}
			writeActuatorJSON(t, w, metricBody("jvm.memory.used", "bytes", map[string]float64{"VALUE": 1024}, nil))
		case r.URL.Path == "/actuator/metrics/jvm.memory.max":
			t.Fatalf("unexpected jvm.memory.max request")
		case r.URL.Path == "/actuator/metrics/process.cpu.usage":
			writeActuatorJSON(t, w, metricBody("process.cpu.usage", nil, map[string]float64{"VALUE": 0.12}, nil))
		case r.URL.Path == "/actuator/metrics/system.cpu.usage":
			writeActuatorJSON(t, w, metricBody("system.cpu.usage", nil, map[string]float64{"VALUE": 0.34}, nil))
		case r.URL.Path == "/actuator/metrics/disk.free":
			writeActuatorJSON(t, w, metricBody("disk.free", "bytes", map[string]float64{"VALUE": 600}, nil))
		case r.URL.Path == "/actuator/metrics/disk.total":
			writeActuatorJSON(t, w, metricBody("disk.total", "bytes", map[string]float64{"VALUE": 1000}, nil))
		case r.URL.Path == "/actuator/metrics/process.start.time":
			writeActuatorJSON(t, w, metricBody("process.start.time", "seconds", map[string]float64{"VALUE": 1700000000}, nil))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	collector := NewSpringActuatorCollector("app", client, true)

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if result.TargetName != "app" {
		t.Fatalf("TargetName = %q, want app", result.TargetName)
	}
	if result.HealthStatus != "UP" || result.DBHealthStatus != "UP" {
		t.Fatalf("health statuses = %q/%q, want UP/UP", result.HealthStatus, result.DBHealthStatus)
	}
	if result.ProcessStartTime == nil || result.ProcessStartTime.Unix() != 1700000000 {
		t.Fatalf("ProcessStartTime = %v, want unix 1700000000", result.ProcessStartTime)
	}
	assertSample(t, result, "http_requests_total", MetricKindCounter, 20, "requests")
	assertSample(t, result, "http_404_total", MetricKindCounter, 3, "requests")
	assertSample(t, result, "http_4xx_total", MetricKindCounter, 3, "requests")
	assertSample(t, result, "http_5xx_total", MetricKindCounter, 2, "requests")
	assertSample(t, result, "http_request_time_total_seconds", MetricKindCounter, 4.2, "seconds")
	assertSample(t, result, "jvm_heap_used_bytes", MetricKindGauge, 1024, "bytes")
	assertSample(t, result, "process_cpu_usage", MetricKindGauge, 0.12, "ratio")
	assertSample(t, result, "host_cpu_usage", MetricKindGauge, 0.34, "ratio")
	assertSample(t, result, "host_disk_used_bytes", MetricKindGauge, 400, "bytes")
	assertSample(t, result, "host_disk_total_bytes", MetricKindGauge, 1000, "bytes")
	assertSample(t, result, "process_start_time", MetricKindGauge, 1700000000, "unix_seconds")
	assertSampleKeys(t, result, []string{
		"http_requests_total",
		"http_404_total",
		"http_4xx_total",
		"http_5xx_total",
		"http_request_time_total_seconds",
		"jvm_heap_used_bytes",
		"process_cpu_usage",
		"host_cpu_usage",
		"host_disk_used_bytes",
		"host_disk_total_bytes",
		"process_start_time",
	})
}

func TestSpringActuatorCollectorRejectsInvalidHostMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actuator/health":
			writeActuatorJSON(t, w, map[string]string{"status": "UP"})
		case "/actuator/metrics/system.cpu.usage":
			writeActuatorJSON(t, w, metricBody("system.cpu.usage", nil, map[string]float64{"VALUE": 1.2}, nil))
		case "/actuator/metrics/disk.free":
			writeActuatorJSON(t, w, metricBody("disk.free", "bytes", map[string]float64{"VALUE": 1200}, nil))
		case "/actuator/metrics/disk.total":
			writeActuatorJSON(t, w, metricBody("disk.total", "bytes", map[string]float64{"VALUE": 1000}, nil))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	result, err := NewSpringActuatorCollector("app", client, true).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	for _, key := range []string{"host_cpu_usage", "host_disk_used_bytes", "host_disk_total_bytes"} {
		if hasSample(result, key) {
			t.Fatalf("unexpected invalid sample %q in %#v", key, result.Samples)
		}
	}
	if countEvents(result, EventSeverityWarning, "metric_invalid") != 2 {
		t.Fatalf("events = %#v, want two metric_invalid warnings", result.Events)
	}
}

func TestSpringActuatorCollectorReportsMissingOptionalMetricsAsWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/actuator/health" {
			writeActuatorJSON(t, w, map[string]string{"status": "UP"})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	collector := NewSpringActuatorCollector("app", client, false)

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.HealthStatus != "UP" {
		t.Fatalf("HealthStatus = %q, want UP", result.HealthStatus)
	}
	if len(result.Samples) != 0 {
		t.Fatalf("Samples length = %d, want 0", len(result.Samples))
	}
	if countEvents(result, EventSeverityWarning, "metric_fetch_failed") != 4 {
		t.Fatalf("warnings = %#v, want four application metric fetch warnings and no disabled host metric requests", result.Events)
	}
}

func TestSpringActuatorCollectorHandlesSparseMetricResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actuator/health":
			writeActuatorJSON(t, w, map[string]string{"status": "UP"})
		case "/actuator/metrics/http.server.requests":
			writeActuatorJSON(t, w, map[string]interface{}{"name": "http.server.requests"})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	collector := NewSpringActuatorCollector("app", client, false)

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.HealthStatus != "UP" {
		t.Fatalf("HealthStatus = %q, want UP", result.HealthStatus)
	}
	if len(result.Samples) != 0 {
		t.Fatalf("Samples length = %d, want 0", len(result.Samples))
	}
	if countEvents(result, EventSeverityWarning, "metric_measurement_missing") < 2 {
		t.Fatalf("events = %#v, want missing measurement warnings", result.Events)
	}
	if countEvents(result, EventSeverityWarning, "metric_tag_missing") != 1 {
		t.Fatalf("events = %#v, want one missing status tag warning", result.Events)
	}
}

func TestSpringActuatorCollectorOmitsInvalidCountersWithoutDroppingValidOnes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/actuator/health":
			writeActuatorJSON(t, w, map[string]string{"status": "UP"})
		case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "":
			writeActuatorJSON(t, w, metricBody("http.server.requests", "seconds", map[string]float64{
				"COUNT":      10,
				"TOTAL_TIME": -5,
			}, map[string][]string{"status": {"404", "500", "501"}}))
		case r.URL.Query().Get("tag") == "status:404":
			writeActuatorJSON(t, w, metricBody("http.server.requests", "seconds", map[string]float64{"COUNT": -1}, nil))
		case r.URL.Query().Get("tag") == "status:500", r.URL.Query().Get("tag") == "status:501":
			writeActuatorJSON(t, w, metricBody("http.server.requests", "seconds", map[string]float64{"COUNT": 1e308}, nil))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	result, err := NewSpringActuatorCollector("app", client, false).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	assertSample(t, result, "http_requests_total", MetricKindCounter, 10, "requests")
	for _, key := range []string{"http_request_time_total_seconds", "http_404_total", "http_4xx_total", "http_5xx_total"} {
		if hasSample(result, key) {
			t.Fatalf("invalid counter %s was retained: %#v", key, result.Samples)
		}
	}
	if countEvents(result, EventSeverityWarning, "metric_invalid") != 3 || countEvents(result, EventSeverityWarning, "metric_aggregate_invalid") != 1 {
		t.Fatalf("events = %#v, want invalid-source and aggregate-overflow diagnostics", result.Events)
	}
}

func TestSpringActuatorCollectorKeepsSamplesOnPartialMetricFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/actuator/health":
			writeActuatorJSON(t, w, map[string]string{"status": "UP"})
		case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "":
			writeActuatorJSON(t, w, metricBody("http.server.requests", "seconds", map[string]float64{
				"COUNT":      10,
				"TOTAL_TIME": 2,
			}, map[string][]string{"status": {"404", "500"}}))
		case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "status:404":
			writeActuatorJSON(t, w, metricBody("http.server.requests", "seconds", map[string]float64{"COUNT": 1}, nil))
		case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "status:500":
			http.Error(w, "backend timeout", http.StatusGatewayTimeout)
		case r.URL.Path == "/actuator/metrics/process.cpu.usage":
			writeActuatorJSON(t, w, metricBody("process.cpu.usage", nil, map[string]float64{"VALUE": 0.5}, nil))
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	collector := NewSpringActuatorCollector("app", client, false)

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	assertSample(t, result, "http_requests_total", MetricKindCounter, 10, "requests")
	assertSample(t, result, "http_404_total", MetricKindCounter, 1, "requests")
	assertSample(t, result, "http_4xx_total", MetricKindCounter, 1, "requests")
	assertSample(t, result, "process_cpu_usage", MetricKindGauge, 0.5, "ratio")
	if countEvents(result, EventSeverityWarning, "metric_fetch_failed") != 3 {
		t.Fatalf("events = %#v, want fetch warnings for failed optional metrics", result.Events)
	}
}

func TestSpringActuatorCollectorReturnsPollErrorWhenHealthFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	collector := NewSpringActuatorCollector("app", client, false)

	result, err := collector.Collect(context.Background())
	if err == nil {
		t.Fatal("Collect() error = nil, want error")
	}
	if countEvents(result, EventSeverityError, "health_fetch_failed") != 1 {
		t.Fatalf("events = %#v, want one health_fetch_failed error", result.Events)
	}
}

func TestSpringActuatorCollectorRecordsUnhealthyHealthFromNon2xxResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/actuator/health" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"status": "DOWN"})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	client, err := NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}

	result, err := NewSpringActuatorCollector("app", client, false).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v, want unhealthy health to be recorded", err)
	}
	if result.HealthStatus != "DOWN" {
		t.Fatalf("HealthStatus = %q, want DOWN", result.HealthStatus)
	}
	if countEvents(result, EventSeverityError, "health_fetch_failed") != 0 {
		t.Fatalf("events = %#v, want no health fetch failure", result.Events)
	}
}

func writeActuatorJSON(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encoding response: %v", err)
	}
}

func metricBody(name string, baseUnit interface{}, measurements map[string]float64, tags map[string][]string) map[string]interface{} {
	body := map[string]interface{}{
		"name": name,
	}
	if baseUnit != nil {
		body["baseUnit"] = baseUnit
	}
	var measurementValues []map[string]interface{}
	for statistic, value := range measurements {
		measurementValues = append(measurementValues, map[string]interface{}{
			"statistic": statistic,
			"value":     value,
		})
	}
	body["measurements"] = measurementValues
	var tagValues []map[string]interface{}
	for tag, values := range tags {
		tagValues = append(tagValues, map[string]interface{}{
			"tag":    tag,
			"values": values,
		})
	}
	body["availableTags"] = tagValues
	return body
}

func assertSample(t *testing.T, result *CollectionResult, key string, kind MetricKind, value float64, unit string) {
	t.Helper()
	for _, sample := range result.Samples {
		if sample.Key == key {
			if sample.Kind != kind || sample.Value != value || sample.Unit != unit {
				t.Fatalf("sample %s = %#v, want kind=%s value=%v unit=%s", key, sample, kind, value, unit)
			}
			return
		}
	}
	var keys []string
	for _, sample := range result.Samples {
		keys = append(keys, sample.Key)
	}
	t.Fatalf("missing sample %s in [%s]", key, strings.Join(keys, ", "))
}

func assertSampleKeys(t *testing.T, result *CollectionResult, want []string) {
	t.Helper()
	got := map[string]bool{}
	for _, sample := range result.Samples {
		got[sample.Key] = true
	}
	if len(got) != len(want) {
		t.Fatalf("sample key count = %d, want %d; samples = %#v", len(got), len(want), result.Samples)
	}
	for _, key := range want {
		if !got[key] {
			t.Fatalf("missing sample key %s; samples = %#v", key, result.Samples)
		}
	}
}

func countEvents(result *CollectionResult, severity EventSeverity, eventType string) int {
	var count int
	for _, event := range result.Events {
		if event.Severity == severity && event.Type == eventType {
			count++
		}
	}
	return count
}
