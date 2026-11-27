package collector

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

func expressHistogram(status int, count int, sum float64) string {
	return fmt.Sprintf(`http_request_duration_seconds_bucket{le="+Inf",status_code="%d"} %d
http_request_duration_seconds_count{status_code="%d"} %d
http_request_duration_seconds_sum{status_code="%d"} %g
`, status, count, status, count, status, sum)
}

func TestExpressContinuity(t *testing.T) {
	transport := &fastAPITestTransport{status: 200}
	client, err := prometheus.NewClientWithTransport(time.Second, prometheus.DefaultLimits, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprintf("runtime=%t", configured), func(t *testing.T) {
			c := NewExpressCollector("app", "http://localhost/metrics", client)
			for _, tc := range []struct {
				name, histogram                  string
				start, requests, missing, errors int
				latency, continuityBreak         bool
			}{
				{"cold", "", 100, 0, 0, 0, false, false},
				{"baseline", expressHistogram(200, 12, 1), 100, 12, 0, 0, true, true},
				{"new statuses", expressHistogram(200, 20, 2) + expressHistogram(404, 1, .1) + expressHistogram(500, 1, .1), 100, 22, 1, 1, true, !configured},
				{"idle scrapes", expressHistogram(200, 20, 2) + expressHistogram(404, 1, .1) + expressHistogram(500, 1, .1), 100, 22, 1, 1, true, false},
				{"masked status reset", expressHistogram(200, 25, 3) + expressHistogram(404, 0, 0) + expressHistogram(500, 1, .1), 100, 26, 0, 1, true, true},
				{"wrong tuple sum", expressHistogram(200, 26, 4) + expressHistogram(404, 0, 0) +
					strings.Replace(expressHistogram(500, 1, .1), `_sum{status_code="500"}`, `_sum{status_code="501"}`, 1), 100, 27, 0, 1, false, false},
				{"latency rebaseline", expressHistogram(200, 27, 5) + expressHistogram(404, 0, 0) + expressHistogram(500, 1, .1), 100, 28, 0, 1, true, false},
				{"masked sum reset", expressHistogram(200, 28, 7) + expressHistogram(404, 0, 0) + expressHistogram(500, 2, .05), 100, 30, 0, 2, false, false},
				{"process replacement with growth", expressHistogram(200, 100, 10) + expressHistogram(404, 1, .1) + expressHistogram(500, 2, .2), 200, 103, 1, 2, true, configured},
			} {
				t.Run(tc.name, func(t *testing.T) {
					transport.body = "up 1\n" + tc.histogram
					if configured {
						transport.body += fmt.Sprintf("process_start_time_seconds %d\nprocess_cpu_seconds_total 11\nnodejs_heap_size_used_bytes 2048\n", tc.start)
						c.process.at = time.Now().Add(-2 * time.Second)
						c.process.cpu.value = 10
					}
					result, err := c.Collect(t.Context())
					if err != nil {
						t.Fatal(err)
					}
					for key, want := range map[string]int{
						"http_requests_total": tc.requests, "http_404_total": tc.missing,
						"http_4xx_total": tc.missing, "http_5xx_total": tc.errors,
					} {
						if got, ok := fastAPISample(result, key); ok != (tc.requests != 0) || got.Value != float64(want) {
							t.Fatalf("%s = %v (present %t), want %d", key, got, ok, want)
						}
					}
					if sample, ok := fastAPISample(result, "http_request_time_total_seconds"); ok != tc.latency || ok && sample.Unit != "seconds" {
						t.Fatalf("unexpected duration: %+v (present %t)", sample, ok)
					}
					if result.CounterContinuityBreak != tc.continuityBreak || result.HealthStatus != "" {
						t.Fatalf("unexpected continuity or health: %+v", result)
					}
					heap, hasHeap := fastAPISample(result, "runtime_heap_used_bytes")
					if hasHeap != configured || hasHeap && (heap.Unit != "bytes" || heap.Value != 2048) ||
						(result.ProcessStartTime != nil) != configured {
						t.Fatalf("unexpected runtime metrics: %+v", result)
					}
					cpu, hasCPU := fastAPISample(result, "process_cpu_usage")
					wantCPU := configured && tc.name != "cold" && tc.start == 100
					if hasCPU != wantCPU || hasCPU && (cpu.Unit != "cores" || cpu.Value < .45 || cpu.Value > .55) {
						t.Fatalf("unexpected CPU: %+v (present %t)", cpu, hasCPU)
					}
				})
			}
		})
	}
}

func TestNodeRoundedProcessStart(t *testing.T) {
	at := time.Unix(100, 600_000_000)
	process := prometheusProcess{startTimeSlack: 500 * time.Millisecond}
	process.accept(prometheus.Sample{Name: "process_start_time_seconds", Value: 101})
	result := &CollectionResult{}
	baseline := process.emit(result, prometheusProcessBaseline{}, at)
	uptime, ok := fastAPISample(result, "process_uptime")
	if baseline.identity != 101 || result.ProcessStartTime == nil || !ok || uptime.Value != 0 {
		t.Fatalf("rounded process timestamp lost its baseline: %+v", result)
	}
	if process.startTime(at.Add(-time.Second)) != nil {
		t.Fatal("accepted a start timestamp outside rounding tolerance")
	}
}

func TestExpressMalformedHistogramLabels(t *testing.T) {
	transport := &fastAPITestTransport{status: 200}
	client, err := prometheus.NewClientWithTransport(time.Second, prometheus.DefaultLimits, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	for _, labels := range []string{`{route="/error",status_code="500"}`, `{status_code="5xx"}`} {
		for _, family := range []string{"_sum", "_count"} {
			t.Run(family+labels, func(t *testing.T) {
				collector := NewExpressCollector("app", "http://localhost/metrics", client)
				transport.body = expressHistogram(404, 1, .1) + expressHistogram(500, 1, .1)
				if _, err := collector.Collect(t.Context()); err != nil {
					t.Fatal(err)
				}
				transport.body = expressHistogram(404, 2, .2) + strings.Replace(
					expressHistogram(500, 3, .3), family+`{status_code="500"}`, family+labels, 1,
				)
				result, err := collector.Collect(t.Context())
				if family == "_count" {
					if err == nil || len(result.Samples) != 0 {
						t.Fatalf("malformed count succeeded: %+v, %v", result, err)
					}
					return
				}
				if err != nil || result.CounterContinuityBreak {
					t.Fatalf("malformed sum interrupted collection: %+v, %v", result, err)
				}
				for key, want := range map[string]float64{
					"http_requests_total": 5, "http_404_total": 2, "http_4xx_total": 2, "http_5xx_total": 3,
				} {
					if got, ok := fastAPISample(result, key); !ok || got.Value != want {
						t.Fatalf("%s = %v (present %t), want %v", key, got, ok, want)
					}
				}
				if _, ok := fastAPISample(result, "http_request_time_total_seconds"); ok {
					t.Fatal("malformed sum produced latency")
				}
			})
		}
	}
}
