package collector

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
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
		code, grouped, valid := pythonStatus(tc.label)
		if code != tc.code || grouped != tc.grouped || valid != tc.valid {
			t.Errorf("pythonStatus(%q) = %d, %v, %v", tc.label, code, grouped, valid)
		}
	}
}

func TestPythonProcess(t *testing.T) {
	at := time.Unix(1000, 0)
	var previous pythonProcessBaseline
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
			var p pythonProcess
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

func TestFastAPIRecordedRecipes(t *testing.T) {
	batches := []struct {
		name                               string
		requests, notFound, client, server int
	}{
		{"scrape_only", 0, 0, 0, 0}, {"200", 3, 0, 0, 0}, {"400", 3, 0, 3, 0},
		{"404", 3, 3, 3, 0}, {"500", 3, 0, 0, 3}, {"unhandled", 3, 0, 0, 3},
		{"slow", 3, 0, 0, 0}, {"mixed", 10, 0, 0, 2},
	}
	for _, recipe := range []string{"default", "configured"} {
		t.Run(recipe, func(t *testing.T) {
			final, err := os.ReadFile("testdata/fastapi/fastapi-" + recipe + ".prom")
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile("testdata/fastapi/fastapi-" + recipe + ".json")
			if err != nil {
				t.Fatal(err)
			}
			var recorded struct {
				Scenarios map[string]struct {
					Deltas [][]json.RawMessage
				}
			}
			if err := json.Unmarshal(data, &recorded); err != nil {
				t.Fatal(err)
			}

			// The spike saved the final raw scrape and ordered batch deltas.
			// Reconstruct intermediate snapshots by undoing each batch.
			snapshots := make([]string, len(batches)+1)
			snapshots[len(batches)] = string(final)
			values := make(map[string]float64)
			for _, line := range strings.Split(string(final), "\n") {
				key, raw, ok := strings.Cut(line, " ")
				if !ok || !fastAPIReplayFamily(key) {
					continue
				}
				value, err := strconv.ParseFloat(raw, 64)
				if err != nil {
					t.Fatal(err)
				}
				values[key] = value
			}
			for i := len(batches) - 1; i >= 0; i-- {
				for _, delta := range recorded.Scenarios[batches[i].name].Deltas {
					var family string
					var labels map[string]string
					var change float64
					for j, dest := range []any{&family, &labels, &change} {
						if err := json.Unmarshal(delta[j], dest); err != nil {
							t.Fatal(err)
						}
					}
					if !fastAPIReplayFamily(family) {
						continue
					}
					var tuple []string
					for key, value := range labels {
						tuple = append(tuple, key+"="+strconv.Quote(value))
					}
					slices.Sort(tuple)
					key := family
					if len(tuple) > 0 {
						key += "{" + strings.Join(tuple, ",") + "}"
					}
					values[key] -= change
					if math.Abs(values[key]) < 1e-10 {
						values[key] = 0
					}
				}
				var body strings.Builder
				for key, value := range values {
					// Drop lazy zero children to exercise first positive tuples.
					if value == 0 && strings.Contains(key, "{") {
						continue
					}
					fmt.Fprintf(&body, "%s %g\n", key, value)
				}
				snapshots[i] = body.String()
			}

			transport := &fastAPITestTransport{status: 200}
			client, err := prometheus.NewClientWithTransport(time.Second, FastAPIPrometheusLimits(), nil, transport)
			if err != nil {
				t.Fatal(err)
			}
			c := NewFastAPICollector("app", "http://localhost/metrics", client)
			poll := func(i int) *CollectionResult {
				t.Helper()
				transport.body = snapshots[i]
				result, err := c.Collect(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			previous := poll(0)
			for i, batch := range batches {
				result := poll(i + 1)
				if batch.name != "scrape_only" && batch.name != "200" && result.CounterContinuityBreak {
					t.Fatalf("%s broke continuity", batch.name)
				}
				for key, want := range map[string]int{
					"http_requests_total": batch.requests, "http_404_total": batch.notFound,
					"http_4xx_total": batch.client, "http_5xx_total": batch.server,
				} {
					old, _ := fastAPISample(previous, key)
					current, ok := fastAPISample(result, key)
					if key == "http_404_total" && recipe == "default" {
						if ok {
							t.Fatalf("%s exact 404 present", batch.name)
						}
						continue
					}
					if (!ok && i > 0) || current.Value-old.Value != float64(want) {
						t.Fatalf("%s %s = %g, want %d (present: %v)", batch.name, key, current.Value-old.Value, want, ok)
					}
				}
				old, _ := fastAPISample(previous, "http_request_time_total_seconds")
				latency, ok := fastAPISample(result, "http_request_time_total_seconds")
				delta := latency.Value - old.Value
				if (batch.requests > 0 && (!ok || delta <= 0)) ||
					(batch.name == "scrape_only" && delta != 0) || (batch.name == "slow" && (delta < 0.45 || delta > 0.60)) {
					t.Fatalf("%s duration delta = %g (present: %v)", batch.name, delta, ok)
				}
				previous = result
			}
		})
	}
}

func fastAPIReplayFamily(key string) bool {
	family, _, _ := strings.Cut(key, "{")
	switch family {
	case fastAPIRequests, fastAPICount, fastAPISum, fastAPISignatureCount, fastAPISignatureSum,
		"process_start_time_seconds", "process_cpu_seconds_total":
		return true
	default:
		return false
	}
}

func TestFastAPIDurationPopulationAndContinuity(t *testing.T) {
	transport := &fastAPITestTransport{status: 200}
	client, err := prometheus.NewClientWithTransport(time.Second, FastAPIPrometheusLimits(), nil, transport)
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
	client, err := prometheus.NewClientWithTransport(time.Second, FastAPIPrometheusLimits(), nil, transport)
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
