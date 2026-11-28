package collector

import (
	"fmt"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

func fastifyHistogram(route string, status, count int, sum float64) string {
	labels := fmt.Sprintf(`method="GET",route=%q,status_code="%d"`, route, status)
	return fmt.Sprintf(`http_request_duration_seconds_bucket{%s,le="+Inf"} %d
http_request_duration_seconds_count{%s} %d
http_request_duration_seconds_sum{%s} %g
http_request_summary_seconds_count{%s} %d
http_request_summary_seconds_sum{%s} %g
`, labels, count, labels, count, labels, sum, labels, count, labels, sum)
}

func TestFastifyRouteScopeAndRestart(t *testing.T) {
	transport := &fastAPITestTransport{status: 200}
	client, err := prometheus.NewClientWithTransport(time.Second, prometheus.DefaultLimits, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	c := NewFastifyCollector("app", "http://localhost/internal/metrics?format=text", client)
	for _, tc := range []struct {
		name                                                  string
		start, scrapes, successes, missing, unmatched, errors int
		broken                                                bool
	}{
		{"default", 100, 500, 8, 1, 0, 2, true},
		{"scrapes only", 100, 600, 8, 1, 0, 2, false},
		{"unmatched routes", 100, 600, 8, 1, 3, 2, false},
		{"replacement", 200, 1, 10, 1, 3, 2, true},
		{"recovered", 200, 20, 11, 1, 3, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport.body = fmt.Sprintf("process_start_time_seconds %d\nprocess_cpu_seconds_total 11\nnodejs_heap_size_used_bytes 2048\n", tc.start) +
				fastifyHistogram("/internal/metrics", 200, tc.scrapes, float64(tc.scrapes)) +
				fastifyHistogram("/ok", 200, tc.successes, float64(tc.successes)/8) +
				fastifyHistogram("/not-found", 404, tc.missing, float64(tc.missing)/8) +
				fastifyHistogram("/crash", 500, tc.errors, float64(tc.errors)/8)
			if tc.unmatched > 0 {
				transport.body += fastifyHistogram("__unknown__", 404, tc.unmatched, float64(tc.unmatched)/8)
			}
			c.process.at = time.Now().Add(-2 * time.Second)
			c.process.cpu.value = 10
			result, err := c.Collect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			requests := tc.successes + tc.missing + tc.unmatched + tc.errors
			for metric, want := range map[string]float64{
				"http_requests_total":             float64(requests),
				"http_404_total":                  float64(tc.missing + tc.unmatched),
				"http_4xx_total":                  float64(tc.missing + tc.unmatched),
				"http_5xx_total":                  float64(tc.errors),
				"http_request_time_total_seconds": float64(requests) / 8,
				"runtime_heap_used_bytes":         2048,
			} {
				if got, ok := fastAPISample(result, metric); !ok || got.Value != want {
					t.Errorf("%s = %v (present %t), want %v", metric, got, ok, want)
				}
			}
			if result.CounterContinuityBreak != tc.broken || result.ProcessStartTime == nil ||
				result.ProcessStartTime.Unix() != int64(tc.start) || result.MetricsSource != FastifyMetricsSource || result.HealthStatus != "" {
				t.Fatalf("unexpected poll metadata: %+v", result)
			}
			if cpu, ok := fastAPISample(result, "process_cpu_usage"); ok == tc.broken ||
				ok && (cpu.Unit != "cores" || cpu.Value < .45 || cpu.Value > .55) {
				t.Errorf("unexpected CPU: %v (present %t)", cpu, ok)
			}
			var scopeWarning bool
			for _, event := range result.Events {
				scopeWarning = scopeWarning || event.Type == "node_route_scope_unverified"
			}
			if scopeWarning != (tc.unmatched == 0) {
				t.Errorf("unexpected route scope events: %+v", result.Events)
			}
		})
	}
}
