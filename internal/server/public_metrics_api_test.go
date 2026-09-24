package server

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestPublicMetricsResponseMapsFixedHourAndResourceUnits(t *testing.T) {
	store := newPublicTestStore(t)
	mon := newServerTestMonitor(t, "app", store, &countingCollector{})
	server := NewWithManager("", mustSingleServerTestManager(t, mon))
	evaluatedAt := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)
	server.FreezeDashboardTime(evaluatedAt)
	run, err := store.EnsureAppRun(t.Context(), "app", nil, evaluatedAt.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	base := evaluatedAt.Add(-time.Hour)
	for _, sample := range []struct {
		at     time.Time
		values map[string]float64
	}{
		{base.Add(-10 * time.Second), map[string]float64{
			"http_requests_total": 0, "http_4xx_total": 0, "http_404_total": 0,
			"http_5xx_total": 0, "http_request_time_total_seconds": 0,
		}},
		{base.Add(10 * time.Second), map[string]float64{
			"http_requests_total": 20, "http_4xx_total": 2, "http_404_total": 1,
			"http_5xx_total": 1, "http_request_time_total_seconds": 4,
			"process_cpu_usage": .4, "runtime_heap_used_bytes": 1000,
			"host_cpu_usage": .7, "host_memory_used_bytes": 70, "host_memory_total_bytes": 100,
			"host_disk_used_bytes": 60, "host_disk_total_bytes": 100,
		}},
		{base.Add(20 * time.Second), map[string]float64{
			"http_requests_total": 40, "http_4xx_total": 3, "http_404_total": 1,
			"http_5xx_total": 3, "http_request_time_total_seconds": 10,
			"process_cpu_usage": .6, "runtime_heap_used_bytes": 3000,
			"host_cpu_usage": .9, "host_memory_used_bytes": 60, "host_memory_total_bytes": 100,
			"host_disk_used_bytes": 80, "host_disk_total_bytes": 100,
		}},
		{base.Add(30 * time.Second), map[string]float64{
			"process_cpu_usage": .8, "runtime_heap_used_bytes": 5000,
			"host_cpu_usage": .8, "host_memory_used_bytes": 80, "host_memory_total_bytes": 100,
			"host_disk_used_bytes": 70, "host_disk_total_bytes": 100,
		}},
	} {
		savePublicMetricsPoll(t, store, run, "app", sample.at, sample.values)
	}
	body := requestPublicMetrics(t, server, "/api/v1/metrics")
	if body.Target != "app" || body.Range != "1h" || body.BucketSeconds != 60 || !body.EvaluatedAt.Equal(evaluatedAt) {
		t.Fatalf("response header = %#v", body)
	}
	if len(body.Points) != 1 {
		t.Fatalf("points = %#v", body.Points)
	}
	point := body.Points[0]
	if !point.Timestamp.Equal(base) {
		t.Fatalf("timestamp = %v", point.Timestamp)
	}
	assertPublicMetricValue(t, "requests", point.Requests, 40)
	assertPublicMetricValue(t, "4xx", point.HTTP4xx, 3)
	assertPublicMetricValue(t, "5xx", point.HTTP5xx, 3)
	assertPublicMetricValue(t, "5xx rate", point.HTTP5xxRate, .075)
	assertPublicMetricValue(t, "latency ms", point.AverageLatencyMS, 250)
	assertPublicMetricValue(t, "latency requests", point.LatencyRequests, 40)
	assertPublicMetricValue(t, "process CPU", point.ProcessCPUCores, .6)
	assertPublicMetricValue(t, "runtime memory", point.RuntimeMemoryBytes, 3000)
	assertPublicMetricValue(t, "host CPU", point.HostCPUUsage, .8)
	assertPublicMetricValue(t, "host memory", point.HostMemoryUsage, .7)
	assertPublicMetricValue(t, "host disk", point.HostDiskUsage, .7)
	if body.LatestHTTPObservationAt == nil || !body.LatestHTTPObservationAt.Equal(base.Add(20*time.Second)) {
		t.Fatalf("HTTP freshness = %v, want latest HTTP poll despite resource-only latest poll", body.LatestHTTPObservationAt)
	}
	for _, forbidden := range []string{"interval_seconds", "poll_id", "app_run_id", "raw_requests", "http_404", "host_memory_used_bytes"} {
		if _, ok := point.raw[forbidden]; ok {
			t.Errorf("metric point exposes %q", forbidden)
		}
	}
}

func TestPublicMetricsEmptyAndRequestBoundaries(t *testing.T) {
	store := newPublicTestStore(t)
	alpha := newServerTestMonitor(t, "alpha", store, &countingCollector{})
	beta := newServerTestMonitor(t, "beta", store, &countingCollector{})
	server := NewWithManager("", newServerTestManager(t, alpha, beta))
	server.FreezeDashboardTime(time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC))
	body := requestPublicMetrics(t, server, "/api/v1/metrics?target=alpha")
	if len(body.Points) != 0 || body.LatestHTTPObservationAt != nil {
		t.Fatalf("empty metrics = %#v", body)
	}
	now := time.Date(2026, 9, 24, 10, 59, 30, 0, time.UTC)
	run, err := store.EnsureAppRun(t.Context(), "alpha", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	savePublicMetricsPoll(t, store, run, "alpha", now, map[string]float64{"process_cpu_usage": .25})
	resourceOnly := requestPublicMetrics(t, server, "/api/v1/metrics?target=alpha")
	if len(resourceOnly.Points) != 1 || resourceOnly.LatestHTTPObservationAt != nil {
		t.Fatalf("resource-only metrics = %#v", resourceOnly)
	}
	assertPublicMetricValue(t, "resource-only CPU", resourceOnly.Points[0].ProcessCPUCores, .25)
	if resourceOnly.Points[0].Requests != nil || resourceOnly.Points[0].HTTP5xxRate != nil {
		t.Fatalf("resource-only HTTP fields = %#v", resourceOnly.Points[0])
	}
	for _, path := range []string{
		"/api/v1/metrics", "/api/v1/metrics?target=missing", "/api/v1/metrics?target=alpha&range=1h",
		"/api/v1/metrics?target=alpha&target=alpha", "/api/v1/metrics?target=",
	} {
		response := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/metrics?target=alpha", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST metrics = %d, Allow %q", response.Code, response.Header().Get("Allow"))
	}
}

func TestPublicMetricsKeepsFreshRequestAndLatencyWhenFiveXXIsMissing(t *testing.T) {
	store := newPublicTestStore(t)
	mon := newServerTestMonitor(t, "app", store, &countingCollector{})
	server := NewWithManager("", mustSingleServerTestManager(t, mon))
	evaluatedAt := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)
	server.FreezeDashboardTime(evaluatedAt)
	base := evaluatedAt.Add(-time.Hour)
	run, err := store.EnsureAppRun(t.Context(), "app", nil, base)
	if err != nil {
		t.Fatal(err)
	}
	savePublicMetricsPoll(t, store, run, "app", base.Add(-time.Second), map[string]float64{
		"http_requests_total": 0, "http_request_time_total_seconds": 0,
	})
	savePublicMetricsPoll(t, store, run, "app", base.Add(10*time.Second), map[string]float64{
		"http_requests_total": 10, "http_request_time_total_seconds": 2,
	})
	savePublicMetricsPoll(t, store, run, "app", base.Add(20*time.Second), map[string]float64{
		"runtime_heap_used_bytes": 100,
	})
	body := requestPublicMetrics(t, server, "/api/v1/metrics")
	if len(body.Points) != 1 {
		t.Fatalf("points = %#v", body.Points)
	}
	p := body.Points[0]
	assertPublicMetricValue(t, "requests", p.Requests, 10)
	assertPublicMetricValue(t, "latency requests", p.LatencyRequests, 10)
	assertPublicMetricValue(t, "latency ms", p.AverageLatencyMS, 200)
	if p.HTTP5xx != nil || p.HTTP5xxRate != nil {
		t.Fatalf("missing 5xx should remain null: %#v", p)
	}
	if body.LatestHTTPObservationAt == nil || !body.LatestHTTPObservationAt.Equal(base.Add(10*time.Second)) {
		t.Fatalf("HTTP freshness = %v", body.LatestHTTPObservationAt)
	}
}

func TestPublicMetricsZeroTrafficKeepsZeroCountsAndNullRateAndLatency(t *testing.T) {
	store := newPublicTestStore(t)
	mon := newServerTestMonitor(t, "app", store, &countingCollector{})
	server := NewWithManager("", mustSingleServerTestManager(t, mon))
	evaluatedAt := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)
	server.FreezeDashboardTime(evaluatedAt)
	base := evaluatedAt.Add(-time.Hour)
	run, err := store.EnsureAppRun(t.Context(), "app", nil, base)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{
		"http_requests_total": 0, "http_4xx_total": 0, "http_5xx_total": 0,
		"http_request_time_total_seconds": 0,
	}
	savePublicMetricsPoll(t, store, run, "app", base.Add(-time.Second), values)
	savePublicMetricsPoll(t, store, run, "app", base.Add(10*time.Second), values)
	body := requestPublicMetrics(t, server, "/api/v1/metrics")
	if len(body.Points) != 1 {
		t.Fatalf("points = %#v", body.Points)
	}
	p := body.Points[0]
	assertPublicMetricValue(t, "requests", p.Requests, 0)
	assertPublicMetricValue(t, "4xx", p.HTTP4xx, 0)
	assertPublicMetricValue(t, "5xx", p.HTTP5xx, 0)
	if p.HTTP5xxRate != nil || p.AverageLatencyMS != nil || p.LatencyRequests != nil {
		t.Fatalf("zero-traffic derived values = %#v", p)
	}
	if body.LatestHTTPObservationAt == nil || !body.LatestHTTPObservationAt.Equal(base.Add(10*time.Second)) {
		t.Fatalf("zero-traffic HTTP freshness = %v", body.LatestHTTPObservationAt)
	}
}

func TestPublicMetricsReadsDuringConcurrentStorageWritesWithoutPolling(t *testing.T) {
	store := newPublicTestStore(t)
	collectorCalls := &countingCollector{}
	mon := newServerTestMonitor(t, "app", store, collectorCalls)
	server := NewWithManagerRetention("", mustSingleServerTestManager(t, mon), 1)
	evaluatedAt := time.Date(2026, 9, 24, 11, 0, 0, 0, time.UTC)
	server.FreezeDashboardTime(evaluatedAt)
	base := evaluatedAt.Add(-time.Hour)
	run, err := store.EnsureAppRun(t.Context(), "app", nil, base)
	if err != nil {
		t.Fatal(err)
	}
	savePublicMetricsPoll(t, store, run, "app", base.Add(-time.Second), map[string]float64{"http_requests_total": 0})
	started := make(chan struct{})
	writeErr := make(chan error, 1)
	go func() {
		close(started)
		for i := 1; i <= 50; i++ {
			at := base.Add(time.Duration(i) * time.Second)
			result := &collector.CollectionResult{
				TargetName: "app", PollStartedAt: at, PollFinishedAt: at.Add(time.Second),
				Samples: []collector.MetricSample{{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: float64(i)}},
			}
			if _, err := store.SaveCollectionResultWithAppRun(context.Background(), result, &run); err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- nil
	}()
	<-started
	for i := 0; i < 20; i++ {
		body := requestPublicMetrics(t, server, "/api/v1/metrics")
		if body.Target != "app" || len(body.Points) > 1 {
			t.Fatalf("inconsistent read = %#v", body)
		}
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
	if collectorCalls.calls.Load() != 0 {
		t.Fatalf("metrics requests triggered %d collections", collectorCalls.calls.Load())
	}
}

func TestPublicMetricPointZeroTrafficAndNulls(t *testing.T) {
	point := publicMetricPoint(storage.PublicMetricPoint{Timestamp: time.Now().UTC(), Requests: floatPointer(0)})
	if point.Requests == nil || *point.Requests != 0 {
		t.Fatalf("requests = %v", point.Requests)
	}
	for name, value := range map[string]*float64{
		"4xx": point.HTTP4xx, "5xx": point.HTTP5xx, "rate": point.HTTP5xxRate,
		"latency": point.AverageLatencyMS, "latency_requests": point.LatencyRequests,
		"cpu": point.ProcessCPUCores, "memory": point.RuntimeMemoryBytes,
		"host_cpu": point.HostCPUUsage, "host_memory": point.HostMemoryUsage, "host_disk": point.HostDiskUsage,
	} {
		if value != nil {
			t.Errorf("%s = %v, want null", name, *value)
		}
	}
}

type decodedPublicMetrics struct {
	Target                  string                     `json:"target"`
	Range                   string                     `json:"range"`
	BucketSeconds           int                        `json:"bucket_seconds"`
	EvaluatedAt             time.Time                  `json:"evaluated_at"`
	LatestHTTPObservationAt *time.Time                 `json:"latest_http_observation_at"`
	Points                  []decodedPublicMetricPoint `json:"points"`
}

type decodedPublicMetricPoint struct {
	Timestamp          time.Time `json:"timestamp"`
	Requests           *float64  `json:"requests"`
	HTTP4xx            *float64  `json:"http_4xx"`
	HTTP5xx            *float64  `json:"http_5xx"`
	HTTP5xxRate        *float64  `json:"http_5xx_rate"`
	AverageLatencyMS   *float64  `json:"avg_latency_ms"`
	LatencyRequests    *float64  `json:"latency_requests"`
	ProcessCPUCores    *float64  `json:"process_cpu_cores"`
	RuntimeMemoryBytes *float64  `json:"runtime_memory_bytes"`
	HostCPUUsage       *float64  `json:"host_cpu_usage"`
	HostMemoryUsage    *float64  `json:"host_memory_usage"`
	HostDiskUsage      *float64  `json:"host_disk_usage"`
	raw                map[string]json.RawMessage
}

func requestPublicMetrics(t *testing.T, server *Server, path string) decodedPublicMetrics {
	t.Helper()
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, response.Code, response.Body.String())
	}
	var body decodedPublicMetrics
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Points []map[string]json.RawMessage `json:"points"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for i := range body.Points {
		body.Points[i].raw = raw.Points[i]
	}
	return body
}

func savePublicMetricsPoll(t *testing.T, store *storage.Store, run int64, target string, at time.Time, values map[string]float64) {
	t.Helper()
	samples := make([]collector.MetricSample, 0, len(values))
	for key, value := range values {
		kind := collector.MetricKindCounter
		unit := "count"
		switch key {
		case "runtime_heap_used_bytes", "jvm_heap_used_bytes", "host_memory_used_bytes", "host_memory_total_bytes", "host_disk_used_bytes", "host_disk_total_bytes":
			kind, unit = collector.MetricKindGauge, "bytes"
		case "process_cpu_usage":
			kind, unit = collector.MetricKindGauge, "cores"
		case "host_cpu_usage":
			kind, unit = collector.MetricKindGauge, "ratio"
		}
		samples = append(samples, collector.MetricSample{Key: key, Kind: kind, Value: value, Unit: unit})
	}
	result := &collector.CollectionResult{TargetName: target, PollStartedAt: at, PollFinishedAt: at.Add(time.Second), Samples: samples}
	if _, err := store.SaveCollectionResultWithAppRun(context.Background(), result, &run); err != nil {
		t.Fatal(err)
	}
}

func floatPointer(value float64) *float64 { return &value }

func assertPublicMetricValue(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}
