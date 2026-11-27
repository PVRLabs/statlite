package collector

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const flaskTestSignature = "flask_exporter_info{version=\"0.23.2\"} 1\n"

func flaskTestHistogram(path, status string, count int, sum float64) string {
	return fmt.Sprintf("%s{method=\"GET\",path=%q,status=%q} %d\n%s{status=%q,path=%q,method=\"GET\"} %g\n",
		flaskCount, path, status, count, flaskSum, status, path, sum)
}

func TestFlaskPathContinuity(t *testing.T) {
	var overLimit strings.Builder
	for i := 0; i <= pythonMaxTuples; i++ {
		overLimit.WriteString(flaskTestHistogram(fmt.Sprintf("/%d", i), "200", 1, 1))
	}
	transport := &fastAPITestTransport{status: 200}
	client, err := prometheus.NewClientWithTransport(time.Second, prometheus.DefaultLimits, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	c := NewFlaskCollector("app", "http://localhost/metrics", client)
	for _, tc := range []struct {
		name, histogram                  string
		requests, start                  int
		wantLatency, wantContinuityBreak bool
	}{
		{"baseline", flaskTestHistogram("/a", "200", 1, 1), 1, 100, true, true},
		{"new path", flaskTestHistogram("/a", "200", 1, 1) + flaskTestHistogram("/b", "200", 1, 1), 2, 100, true, false},
		{"removed path masked by growth", flaskTestHistogram("/b", "200", 3, 3), 3, 100, false, false},
		{"rebaseline", flaskTestHistogram("/b", "200", 4, 4), 4, 100, true, false},
		{"path counter drops despite aggregate growth", flaskTestHistogram("/b", "200", 3, 3) + flaskTestHistogram("/c", "200", 2, 2), 5, 100, false, false},
		{"rebaseline after drop", flaskTestHistogram("/b", "200", 4, 4) + flaskTestHistogram("/c", "200", 2, 2), 6, 100, true, false},
		{"sum changes without observations", flaskTestHistogram("/b", "200", 4, 5) + flaskTestHistogram("/c", "200", 3, 3), 7, 100, false, false},
		{"limit", overLimit.String(), pythonMaxTuples + 1, 100, false, false},
		{"restart with growth", flaskTestHistogram("/new", "200", 1000, 100), 1000, 200, true, true},
		{"after restart", flaskTestHistogram("/new", "200", 1001, 101), 1001, 200, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport.body = flaskTestSignature + tc.histogram +
				fmt.Sprintf("process_start_time_seconds %d\nflask_http_request_total{method=\"GET\",status=\"200\"} %d\n", tc.start, tc.requests)
			result, err := c.Collect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			requests, present := fastAPISample(result, "http_requests_total")
			_, latency := fastAPISample(result, "http_request_time_total_seconds")
			if !present || requests.Value != float64(tc.requests) || latency != tc.wantLatency ||
				result.CounterContinuityBreak != tc.wantContinuityBreak {
				t.Fatalf("unexpected poll: %+v", result)
			}
			if len(c.previousDurations) > pythonMaxTuples || !latency && c.previousDurations != nil {
				t.Fatalf("retained %d duration tuples", len(c.previousDurations))
			}
		})
	}
}

func TestFlaskHistogramScope(t *testing.T) {
	const requests = `flask_http_request_total{method="GET",status="200"} 2
flask_http_request_total{method="GET",status="500"} 1
flask_http_request_exceptions_total{method="GET",status="500"} 1
`
	transport := &fastAPITestTransport{status: 200}
	client, err := prometheus.NewClientWithTransport(time.Second, prometheus.DefaultLimits, nil, transport)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, histograms string
		valid            bool
	}{
		{"matched", flaskTestHistogram("/ok", "200", 2, .25) + flaskTestHistogram("/crash", "500", 1, .25), true},
		{"matching overall count but wrong statuses", flaskTestHistogram("/ok", "200", 1, .25) + flaskTestHistogram("/crash", "500", 2, .25), false},
		{"sum and count from different paths", "flask_http_request_duration_seconds_count{method=\"GET\",path=\"/a\",status=\"200\"} 2\n" +
			"flask_http_request_duration_seconds_sum{method=\"GET\",path=\"/b\",status=\"200\"} 0.25\n" + flaskTestHistogram("/crash", "500", 1, .25), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport.body = flaskTestSignature + requests + tc.histograms
			result, err := NewFlaskCollector("app", "http://localhost/metrics", client).Collect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]float64{"http_requests_total": 3, "http_5xx_total": 1, "http_4xx_total": 0, "http_404_total": 0} {
				got, present := fastAPISample(result, key)
				if !present || got.Value != want {
					t.Fatalf("%s = %v (present %t), want %v", key, got.Value, present, want)
				}
			}
			latency, present := fastAPISample(result, "http_request_time_total_seconds")
			if present != tc.valid || present && latency.Value != .5 {
				t.Fatalf("duration = %+v (present %t)", latency, present)
			}
		})
	}
}
