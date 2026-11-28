package collector

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

func TestPythonStatus(t *testing.T) {
	for _, tc := range []struct {
		label          string
		code           int
		grouped, valid bool
	}{
		{"404", 404, false, true}, {"5xx", 500, true, true}, {"1xx", 100, true, true},
		{"599", 599, false, true}, {"0", 0, false, false}, {"6xx", 0, false, false},
		{"5XX", 0, false, false}, {"4x4", 0, false, false}, {" 404", 0, false, false},
	} {
		code, grouped, valid := prometheusHTTPStatus(tc.label)
		if code != tc.code || grouped != tc.grouped || valid != tc.valid {
			t.Errorf("prometheusHTTPStatus(%q) = %d, %v, %v", tc.label, code, grouped, valid)
		}
	}
}

func TestPythonProcess(t *testing.T) {
	at := time.Unix(1000, 0)
	var previous prometheusProcessBaseline
	for _, tc := range []struct {
		name               string
		start, cpu         float64
		ambiguous, wantCPU bool
		want               float64
	}{
		{"baseline", 100.25, 5, false, false, 0},
		{"two cores", 100.25, 9, false, true, 2},
		{"idle", 100.25, 9, false, true, 0},
		{"counter drop", 100.25, 1, false, false, 0},
		{"restart with growth", 200.25, 30, false, false, 0},
		{"ambiguous CPU", 200.25, 31, true, false, 0},
		{"missing baseline", 200.25, 32, false, false, 0},
		{"missing start", 0, 33, false, false, 0},
		{"start returns", 200.25, 34, false, false, 0},
		{"invalid CPU", 200.25, math.NaN(), false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at = at.Add(2 * time.Second)
			var p prometheusProcess
			if tc.start > 0 {
				p.accept(prometheus.Sample{Name: "process_start_time_seconds", Value: tc.start})
			}
			p.accept(prometheus.Sample{Name: "process_cpu_seconds_total", Value: tc.cpu})
			if tc.ambiguous {
				p.accept(prometheus.Sample{Name: "process_cpu_seconds_total", Value: tc.cpu})
			}
			var result CollectionResult
			previous = p.emit(&result, previous, at)
			cpu, ok := fastAPISample(&result, "process_cpu_usage")
			if ok != tc.wantCPU || ok && (cpu.Value != tc.want || cpu.Unit != "cores") {
				t.Fatalf("CPU = %+v (%v), want %v (%v)", cpu, ok, tc.want, tc.wantCPU)
			}
			if tc.start > 0 && (result.ProcessStartTime == nil || result.ProcessStartTime.Nanosecond() != 250000000) {
				t.Fatalf("process start = %v", result.ProcessStartTime)
			}
		})
	}
}

type fastAPITestTransport struct {
	body   string
	status int
}

func (t *fastAPITestTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: t.status, Header: http.Header{"Content-Type": {"text/plain"}},
		Body: io.NopCloser(strings.NewReader(t.body))}, nil
}

func fastAPISample(result *CollectionResult, key string) (MetricSample, bool) {
	for _, sample := range result.Samples {
		if sample.Key == key {
			return sample, true
		}
	}
	return MetricSample{}, false
}

func TestFastAPISyntheticRecipes(t *testing.T) {
	for _, recipe := range []struct {
		name                      string
		ok, bad, missing, failure string
		exact                     bool
	}{
		{"grouped", "2xx", "4xx", "4xx", "5xx", false},
		{"exact", "200", "400", "404", "500", true},
	} {
		t.Run(recipe.name, func(t *testing.T) {
			transport := &fastAPITestTransport{status: 200}
			client, err := prometheus.NewClientWithTransport(time.Second, DirectRoutePrometheusLimits(), nil, transport)
			if err != nil {
				t.Fatal(err)
			}
			c := NewFastAPICollector("app", "http://localhost/metrics", client)
			poll := func(traffic, scrapes int) *CollectionResult {
				t.Helper()
				var body strings.Builder
				body.WriteString("http_request_duration_highr_seconds_count 100\nhttp_request_duration_highr_seconds_sum 10\nprocess_start_time_seconds 100\n")
				route := func(handler, status string, count int, duration float64) {
					fmt.Fprintf(&body, "http_requests_total{handler=%q,method=\"GET\",status=%q} %d\n", handler, status, count)
					fmt.Fprintf(&body, "http_request_duration_seconds_count{handler=%q,method=\"GET\"} %d\n", handler, count)
					fmt.Fprintf(&body, "http_request_duration_seconds_sum{handler=%q,method=\"GET\"} %g\n", handler, duration)
				}
				route("/ok", recipe.ok, 10+8*traffic, 1+float64(traffic))
				if !recipe.exact {
					route("/metrics", recipe.ok, scrapes, float64(scrapes))
				}
				if traffic > 0 {
					route("/error", recipe.failure, traffic, 0.25*float64(traffic))
					route("/crash", recipe.failure, traffic, 0.25*float64(traffic))
					route("/bad", recipe.bad, traffic, 0.25*float64(traffic))
					route("none", recipe.missing, traffic, 0.25*float64(traffic))
				}
				transport.body = body.String()
				result, err := c.Collect(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			assertDelta := func(before, after *CollectionResult, requests, client, server, notFound, duration float64) {
				t.Helper()
				if after.CounterContinuityBreak {
					t.Fatal("unexpected continuity break")
				}
				for key, want := range map[string]float64{
					"http_requests_total": requests, "http_4xx_total": client,
					"http_5xx_total": server, "http_404_total": notFound,
					"http_request_time_total_seconds": duration,
				} {
					old, hadOld := fastAPISample(before, key)
					new, hasNew := fastAPISample(after, key)
					if key == "http_404_total" && !recipe.exact {
						if hadOld || hasNew {
							t.Fatalf("grouped status exposed exact 404: %v, %v", old, new)
						}
						continue
					}
					if !hadOld || !hasNew || new.Value-old.Value != want {
						t.Fatalf("%s delta = %g, want %g (before: %v, after: %v)", key, new.Value-old.Value, want, hadOld, hasNew)
					}
				}
			}
			initial := poll(0, 1)
			idle := poll(0, 5)
			assertDelta(initial, idle, 0, 0, 0, 0, 0)
			active := poll(1, 6)
			assertDelta(idle, active, 12, 2, 2, 1, 2)
		})
	}
}

func TestFastAPIDurationPopulationAndContinuity(t *testing.T) {
	transport := &fastAPITestTransport{status: 200}
	client, err := prometheus.NewClientWithTransport(time.Second, DirectRoutePrometheusLimits(), nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	c := NewFastAPICollector("app", "http://localhost/metrics", client)
	scrape := func(start, requests, durationCount int, sum float64, wantBreak, wantDuration bool) {
		t.Helper()
		transport.body = fmt.Sprintf(`http_request_duration_highr_seconds_count 100
http_request_duration_highr_seconds_sum 10
process_start_time_seconds %d
http_requests_total{handler="/ok",method="GET",status="200"} %d
http_request_duration_seconds_count{handler="/ok",method="GET"} %d
http_request_duration_seconds_sum{handler="/ok",method="GET"} %g
`, start, requests, durationCount, sum)
		result, err := c.Collect(t.Context())
		if err != nil || result.CounterContinuityBreak != wantBreak {
			t.Fatalf("result = %+v, err = %v", result, err)
		}
		if _, ok := fastAPISample(result, "http_request_time_total_seconds"); ok != wantDuration {
			t.Fatalf("duration present=%v, want %v", ok, wantDuration)
		}
		if _, ok := fastAPISample(result, "http_requests_total"); !ok {
			t.Fatal("missing valid HTTP requests")
		}
	}
	scrape(100, 10, 10, 1, true, true)
	scrape(100, 11, 10, 1, false, false)
	scrape(100, 12, 12, 2, false, true)
	scrape(100, 13, 13, 1, false, false)
	scrape(100, 14, 14, 2, false, true)
	scrape(200, 20, 20, 5, true, true)
	scrape(200, 21, 21, 6, false, true)
	transport.status = 503
	if _, err := c.Collect(t.Context()); err == nil {
		t.Fatal("expected scrape failure")
	}
	transport.status = 200
	scrape(200, 22, 22, 7, true, true)
}

func TestFastAPILatencyRejectsWrongRouteDenominators(t *testing.T) {
	transport := &fastAPITestTransport{status: 200, body: `http_request_duration_highr_seconds_count 10
http_request_duration_highr_seconds_sum 1
http_requests_total{handler="/ok",method="GET",status="200"} 8
http_requests_total{handler="none",method="POST",status="404"} 2
http_request_duration_seconds_count{handler="/ok",method="GET"} 2
http_request_duration_seconds_sum{handler="/ok",method="GET"} 0.2
http_request_duration_seconds_count{handler="none",method="POST"} 8
http_request_duration_seconds_sum{handler="none",method="POST"} 0.8
`}
	client, err := prometheus.NewClientWithTransport(time.Second, DirectRoutePrometheusLimits(), nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewFastAPICollector("app", "http://localhost/metrics", client).Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fastAPISample(result, "http_request_time_total_seconds"); ok {
		t.Fatal("equal aggregate duration count hid incompatible route counts")
	}
	if requests, ok := fastAPISample(result, "http_requests_total"); !ok || requests.Value != 10 {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestFastAPIMultiprocessAggregate(t *testing.T) {
	transport := &fastAPITestTransport{status: 200}
	client, err := prometheus.NewClientWithTransport(time.Second, DirectRoutePrometheusLimits(), nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	const endpoint = "http://localhost/metrics"
	c := NewFastAPICollector("app", endpoint, client)
	for _, tc := range []struct {
		name       string
		ok, failed int
		wantBreak  bool
	}{
		{"warm aggregate", 1, 1, true},
		{"before replacement", 8, 4, false},
		{"idle replacement", 8, 4, false},
		{"after replacement", 15, 7, false},
		{"fresh directory with visible reset", 2, 1, true},
		{"after reset", 9, 4, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			total := tc.ok + tc.failed
			transport.body = fmt.Sprintf(`http_request_duration_highr_seconds_count %[3]d
http_request_duration_highr_seconds_sum %[3]d
http_requests_total{handler="/ok",method="GET",status="200"} %[1]d
http_requests_total{handler="/error",method="GET",status="500"} %[2]d
http_request_duration_seconds_count{handler="/ok",method="GET"} %[1]d
http_request_duration_seconds_sum{handler="/ok",method="GET"} %[1]d
http_request_duration_seconds_count{handler="/error",method="GET"} %[2]d
http_request_duration_seconds_sum{handler="/error",method="GET"} %[2]d
`, tc.ok, tc.failed, total)
			result, err := c.Collect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if result.CounterContinuityBreak != tc.wantBreak || result.ProcessStartTime != nil {
				t.Fatalf("unexpected continuity or process identity: %+v", result)
			}
			for key, want := range map[string]int{
				"http_requests_total": total, "http_5xx_total": tc.failed,
				"http_4xx_total": 0, "http_404_total": 0,
				"http_request_time_total_seconds": total,
			} {
				if sample, ok := fastAPISample(result, key); !ok || sample.Value != float64(want) {
					t.Errorf("%s = %+v, want %d", key, sample, want)
				}
			}
			if len(result.Samples) != 5 {
				t.Fatalf("expected only the five HTTP samples: %+v", result.Samples)
			}
		})
	}
	capabilities, warnings, err := InspectFastAPI(t.Context(), endpoint, client)
	if err != nil || len(warnings) != 0 || strings.Join(capabilities, ",") != "metrics,requests,http_4xx,http_5xx,http_404,latency" {
		t.Fatalf("unexpected aggregate inspection: %v, %v, %v", capabilities, warnings, err)
	}
}
