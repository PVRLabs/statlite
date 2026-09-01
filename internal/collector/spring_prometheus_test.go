package collector

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

func TestSpringAutoSelectsPrometheusOnceAndNormalizes(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/actuator/health":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"UP"}`)
		case "/actuator/prometheus":
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			fmt.Fprint(w, "process_start_time_seconds 1700000000\nhttp_server_requests_seconds_count{status=\"200\"} 8\nhttp_server_requests_seconds_count{status=\"404\"} 2\nhttp_server_requests_seconds_sum 3.5\njvm_memory_used_bytes{area=\"heap\",id=\"a\"} 100\njvm_memory_used_bytes{area=\"heap\",id=\"b\"} 50\n")
		default:
			http.Error(w, "Actuator metrics must not be used", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	c := newConfiguredSpringTestCollector(t, server.URL+"/actuator", SpringMetricsSourceAuto)
	for i := 0; i < 2; i++ {
		result, err := c.Collect(context.Background())
		if err != nil {
			t.Fatalf("Collect() error = %v", err)
		}
		assertSample(t, result, "http_requests_total", MetricKindCounter, 10, "requests")
		assertSample(t, result, "http_404_total", MetricKindCounter, 2, "requests")
		assertSample(t, result, "jvm_heap_used_bytes", MetricKindGauge, 150, "bytes")
	}
	if requests["/actuator/prometheus"] != 2 {
		t.Fatalf("prometheus requests = %d, want 2", requests["/actuator/prometheus"])
	}
}

func TestSpringActuatorAndPrometheusNormalizeHTTPCountersEquivalently(t *testing.T) {
	tests := []struct {
		name     string
		statuses []string
		counts   map[string]float64
	}{
		{name: "successful requests only", statuses: []string{"200"}, counts: map[string]float64{"200": 20}},
		{name: "error status aggregation", statuses: []string{"200", "404", "500"}, counts: map[string]float64{"200": 15, "404": 3, "500": 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/actuator/health":
					fmt.Fprint(w, `{"status":"UP"}`)
				case "/actuator/prometheus":
					w.Header().Set("Content-Type", "text/plain; version=0.0.4")
					fmt.Fprint(w, "process_start_time_seconds 1700000000\n")
					for _, status := range tt.statuses {
						fmt.Fprintf(w, "http_server_requests_seconds_count{status=%q} %v\n", status, tt.counts[status])
					}
					fmt.Fprint(w, "http_server_requests_seconds_sum 4.2\n")
				case "/actuator/metrics/http.server.requests":
					if tag := r.URL.Query().Get("tag"); tag != "" {
						status := strings.TrimPrefix(tag, "status:")
						fmt.Fprintf(w, `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":%v}]}`, tt.counts[status])
						return
					}
					fmt.Fprintf(w, `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":20},{"statistic":"TOTAL_TIME","value":4.2}],"availableTags":[{"tag":"status","values":[%s]}]}`, quotedJSONStrings(tt.statuses))
				case "/actuator/metrics/process.start.time":
					fmt.Fprint(w, `{"name":"process.start.time","measurements":[{"statistic":"VALUE","value":1700000000}]}`)
				default:
					http.Error(w, "missing", http.StatusNotFound)
				}
			}))
			defer server.Close()

			actuator, err := newConfiguredSpringTestCollector(t, server.URL+"/actuator", SpringMetricsSourceActuator).Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			prometheusResult, err := newConfiguredSpringTestCollector(t, server.URL+"/actuator", SpringMetricsSourcePrometheus).Collect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"http_requests_total", "http_request_time_total_seconds", "http_404_total", "http_4xx_total", "http_5xx_total"} {
				gotActuator, gotPrometheus := sampleByKey(actuator.Samples, key), sampleByKey(prometheusResult.Samples, key)
				if gotActuator == nil || gotPrometheus == nil || *gotActuator != *gotPrometheus {
					t.Fatalf("%s actuator=%#v prometheus=%#v", key, gotActuator, gotPrometheus)
				}
			}
		})
	}
}

func quotedJSONStrings(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = fmt.Sprintf("%q", value)
	}
	return strings.Join(quoted, ",")
}

func TestSpringPrometheusEmitsZeroHTTPErrorCounters(t *testing.T) {
	v := &springPrometheusValues{sawHTTPCount: true, completeHTTPStatusLabels: true, values: map[string]float64{"http_requests_total": 8}}
	result := &CollectionResult{}
	(&SpringActuatorCollector{}).addPrometheusSamples(result, v)
	for _, key := range []string{"http_404_total", "http_4xx_total", "http_5xx_total"} {
		assertSample(t, result, key, MetricKindCounter, 0, "requests")
	}
}

func TestSpringPrometheusOmitsStatusCountersWhenLabelsAreMissing(t *testing.T) {
	v := &springPrometheusValues{values: make(map[string]float64)}
	if err := v.accept(prometheus.Sample{Name: "http_server_requests_seconds_count", Value: 8}, false); err != nil {
		t.Fatal(err)
	}
	result := &CollectionResult{}
	(&SpringActuatorCollector{}).addPrometheusSamples(result, v)
	assertSample(t, result, "http_requests_total", MetricKindCounter, 8, "requests")
	for _, key := range []string{"http_404_total", "http_4xx_total", "http_5xx_total"} {
		if sampleByKey(result.Samples, key) != nil {
			t.Fatalf("%s should be omitted", key)
		}
	}
	if countEvents(result, EventSeverityWarning, "metric_tag_missing") != 1 {
		t.Fatalf("events = %#v", result.Events)
	}
}

func TestSpringPrometheusOmitsInvalidCountersWithoutDroppingValidConcepts(t *testing.T) {
	for _, value := range []float64{-5, math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(fmt.Sprintf("value=%v", value), func(t *testing.T) {
			v := &springPrometheusValues{values: make(map[string]float64)}
			if err := v.accept(prometheus.Sample{Name: "http_server_requests_seconds_count", Value: value, Labels: []prometheus.Label{{Name: "status", Value: "500"}}}, false); err != nil {
				t.Fatal(err)
			}
			if err := v.accept(prometheus.Sample{Name: "http_server_requests_seconds_count", Value: 2, Labels: []prometheus.Label{{Name: "status", Value: "404"}}}, false); err != nil {
				t.Fatal(err)
			}
			if err := v.accept(prometheus.Sample{Name: "http_server_requests_seconds_sum", Value: value}, false); err != nil {
				t.Fatal(err)
			}
			if err := v.accept(prometheus.Sample{Name: "process_cpu_usage", Value: 0.25}, false); err != nil {
				t.Fatal(err)
			}

			result := &CollectionResult{}
			(&SpringActuatorCollector{}).addPrometheusSamples(result, v)
			if hasSample(result, "http_requests_total") || hasSample(result, "http_5xx_total") || hasSample(result, "http_request_time_total_seconds") {
				t.Fatalf("invalid counter samples were retained: %#v", result.Samples)
			}
			assertSample(t, result, "http_404_total", MetricKindCounter, 2, "requests")
			assertSample(t, result, "http_4xx_total", MetricKindCounter, 2, "requests")
			assertSample(t, result, "process_cpu_usage", MetricKindGauge, 0.25, "ratio")
		})
	}
}

func TestSpringPrometheusOmitsCounterAggregateOverflow(t *testing.T) {
	v := &springPrometheusValues{values: make(map[string]float64)}
	for _, sample := range []prometheus.Sample{
		{Name: "http_server_requests_seconds_count", Value: 1e308, Labels: []prometheus.Label{{Name: "status", Value: "500"}}},
		{Name: "http_server_requests_seconds_count", Value: 1e308, Labels: []prometheus.Label{{Name: "status", Value: "500"}}},
		{Name: "http_server_requests_seconds_count", Value: 2, Labels: []prometheus.Label{{Name: "status", Value: "404"}}},
	} {
		if err := v.accept(sample, false); err != nil {
			t.Fatal(err)
		}
	}

	result := &CollectionResult{}
	(&SpringActuatorCollector{}).addPrometheusSamples(result, v)
	if hasSample(result, "http_requests_total") || hasSample(result, "http_5xx_total") {
		t.Fatalf("overflowed counter samples were retained: %#v", result.Samples)
	}
	assertSample(t, result, "http_404_total", MetricKindCounter, 2, "requests")
	assertSample(t, result, "http_4xx_total", MetricKindCounter, 2, "requests")
	if countEvents(result, EventSeverityWarning, "metric_aggregate_invalid") != 2 {
		t.Fatalf("events = %#v, want request and 5xx aggregate warnings", result.Events)
	}
}

func TestSpringPrometheusRejectsOutOfRangeHostCPU(t *testing.T) {
	for _, value := range []float64{-0.2, 1.5} {
		result := &CollectionResult{}
		v := &springPrometheusValues{values: map[string]float64{"host_cpu_usage": value}}
		(&SpringActuatorCollector{}).addPrometheusSamples(result, v)
		if sampleByKey(result.Samples, "host_cpu_usage") != nil {
			t.Fatalf("host_cpu_usage %v was retained", value)
		}
		if countEvents(result, EventSeverityWarning, "metric_invalid") != 1 {
			t.Fatalf("events = %#v, want metric_invalid", result.Events)
		}
	}
}

func sampleByKey(samples []MetricSample, key string) *MetricSample {
	for i := range samples {
		if samples[i].Key == key {
			return &samples[i]
		}
	}
	return nil
}

func TestSpringAutoFallsBackOnlyOnDefinitiveResults(t *testing.T) {
	tests := []struct {
		name              string
		prometheusStatus  int
		body, contentType string
		wantPrometheus    int
		wantActuator      int
	}{
		{name: "absent", prometheusStatus: http.StatusNotFound, body: "missing", wantPrometheus: 1, wantActuator: 1},
		{name: "valid incompatible", prometheusStatus: http.StatusOK, body: "unrelated_metric 1\n", contentType: "text/plain; version=0.0.4", wantPrometheus: 1, wantActuator: 1},
		{name: "process start only is incompatible", prometheusStatus: http.StatusOK, body: "process_start_time_seconds 1\n", contentType: "text/plain; version=0.0.4", wantPrometheus: 1, wantActuator: 1},
		{name: "invalid counter is incompatible", prometheusStatus: http.StatusOK, body: "process_start_time_seconds 1\nhttp_server_requests_seconds_count{status=\"200\"} -5\n", contentType: "text/plain; version=0.0.4", wantPrometheus: 1, wantActuator: 1},
		{name: "malformed remains unresolved", prometheusStatus: http.StatusOK, body: "bad", contentType: "text/plain; version=0.0.4", wantPrometheus: 2},
		{name: "auth remains unresolved", prometheusStatus: http.StatusUnauthorized, body: "denied", wantPrometheus: 2},
		{name: "transient remains unresolved", prometheusStatus: http.StatusServiceUnavailable, body: "later", wantPrometheus: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var prom, actuator int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/actuator/health":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"status":"UP"}`)
				case "/actuator/prometheus":
					prom++
					if tt.contentType != "" {
						w.Header().Set("Content-Type", tt.contentType)
					}
					w.WriteHeader(tt.prometheusStatus)
					fmt.Fprint(w, tt.body)
				case "/actuator/metrics/http.server.requests":
					actuator++
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":4},{"statistic":"TOTAL_TIME","value":1}]}`)
				default:
					w.Header().Set("Content-Type", "application/json")
					http.Error(w, "missing", http.StatusNotFound)
				}
			}))
			defer server.Close()
			c := newConfiguredSpringTestCollector(t, server.URL+"/actuator", SpringMetricsSourceAuto)
			for i := 0; i < 2; i++ {
				result, err := c.Collect(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				if tt.wantActuator == 0 && len(result.Events) == 0 {
					t.Fatal("unresolved source missing warning")
				}
			}
			if prom != tt.wantPrometheus {
				t.Fatalf("prometheus requests = %d, want %d", prom, tt.wantPrometheus)
			}
			if actuator != tt.wantActuator*2 {
				t.Fatalf("actuator metric requests = %d, want %d", actuator, tt.wantActuator*2)
			}
		})
	}
}

func TestSpringCommittedPrometheusFailureDoesNotSwitch(t *testing.T) {
	var scrapes, actuator int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actuator/health":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"UP"}`)
		case "/actuator/prometheus":
			scrapes++
			if scrapes > 1 {
				http.Error(w, "later", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			fmt.Fprint(w, "process_start_time_seconds 1\nprocess_cpu_usage 0.1\n")
		default:
			actuator++
			http.Error(w, "must not switch", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	c := newConfiguredSpringTestCollector(t, server.URL+"/actuator", SpringMetricsSourceAuto)
	if _, err := c.Collect(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("metrics failure should be partial: %v", err)
	}
	if result.HealthStatus != "UP" || len(result.Events) == 0 {
		t.Fatalf("partial result = %#v", result)
	}
	if actuator != 0 {
		t.Fatalf("actuator requests = %d, want 0", actuator)
	}
}

func TestSpringExplicitMetricsSourcesDoNotProbeTheOtherSource(t *testing.T) {
	for _, source := range []SpringMetricsSource{SpringMetricsSourcePrometheus, SpringMetricsSourceActuator} {
		t.Run(string(source), func(t *testing.T) {
			var prometheusRequests, actuatorRequests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/actuator/health":
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"status":"UP"}`)
				case "/actuator/prometheus":
					prometheusRequests++
					w.Header().Set("Content-Type", "text/plain; version=0.0.4")
					fmt.Fprint(w, "process_start_time_seconds 1\nprocess_cpu_usage 0.1\n")
				case "/actuator/metrics/http.server.requests":
					actuatorRequests++
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":1}]}`)
				default:
					w.Header().Set("Content-Type", "application/json")
					http.Error(w, "missing", http.StatusNotFound)
				}
			}))
			defer server.Close()
			if _, err := newConfiguredSpringTestCollector(t, server.URL+"/actuator", source).Collect(context.Background()); err != nil {
				t.Fatal(err)
			}
			if source == SpringMetricsSourcePrometheus && (prometheusRequests != 1 || actuatorRequests != 0) {
				t.Fatalf("requests prometheus=%d actuator=%d", prometheusRequests, actuatorRequests)
			}
			if source == SpringMetricsSourceActuator && (prometheusRequests != 0 || actuatorRequests != 1) {
				t.Fatalf("requests prometheus=%d actuator=%d", prometheusRequests, actuatorRequests)
			}
		})
	}
}

func TestSpringExplicitPrometheusReportsIncompatibleScrapeWithoutFallback(t *testing.T) {
	var actuatorRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actuator/health":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"UP"}`)
		case "/actuator/prometheus":
			w.Header().Set("Content-Type", "text/plain; version=0.0.4")
			fmt.Fprint(w, "process_start_time_seconds 1\nunrelated_metric 2\n")
		default:
			actuatorRequests++
			http.Error(w, "must not fall back", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	result, err := newConfiguredSpringTestCollector(t, server.URL+"/actuator", SpringMetricsSourcePrometheus).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.HealthStatus != "UP" || len(result.Samples) != 0 {
		t.Fatalf("partial result = %#v", result)
	}
	if countEvents(result, EventSeverityWarning, "metrics_source_incompatible") != 1 {
		t.Fatalf("events = %#v", result.Events)
	}
	if actuatorRequests != 0 {
		t.Fatalf("actuator metric requests = %d, want 0", actuatorRequests)
	}
}

func newConfiguredSpringTestCollector(t *testing.T, base string, source SpringMetricsSource) *SpringActuatorCollector {
	t.Helper()
	actuator, err := NewActuatorClient(base, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	prom, err := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewSpringCollector("app", actuator, prom, strings.TrimRight(base, "/")+"/prometheus", source, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
