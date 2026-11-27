package collector

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/prometheus"
)

func TestDjangoInlineFiltersScrapeAndCountsCompletedResponses(t *testing.T) {
	c, setBody := djangoTestCollector(t, config.DjangoMetricsModeInline)
	setBody(`django_http_responses_total_by_status_view_method_total{status="200",view="index",method="GET"} 1
django_http_responses_total_by_status_view_method_total{status="200",view="prometheus-django-metrics",method="GET"} 5
`)
	assertDjangoSamples(t, pollDjango(t, c), 1, 0, 0, 0, -1)
	setBody(`django_http_responses_total_by_status_view_method_total{status="200",view="index",method="GET"} 8
django_http_responses_total_by_status_view_method_total{status="200",view="prometheus-django-metrics",method="GET"} 9
django_http_responses_total_by_status_view_method_total{status="404",view="",method=""} 1
django_http_responses_total_by_status_view_method_total{status="500",view="crash",method="GET"} 2
django_http_requests_total_by_view_transport_method_total{view="index",transport="http",method="GET"} 8
django_http_requests_total_by_view_transport_method_total{view="crash",transport="http",method="GET"} 2
django_http_requests_latency_seconds_by_view_method_count{view="crash",method="GET"} 4
django_http_requests_latency_seconds_by_view_method_sum{view="crash",method="GET"} 0.8
`)
	result := pollDjango(t, c)
	if result.CounterContinuityBreak {
		t.Fatal("new status tuples should count within the same process")
	}
	assertDjangoSamples(t, result, 11, 1, 1, 2, -1)
}

func TestDjangoSeparateLatencyContinuity(t *testing.T) {
	c, setBody := djangoTestCollector(t, config.DjangoMetricsModeSeparate)
	for _, step := range []struct {
		name, extra     string
		requests, count int
		sum, want       float64
		restart         bool
	}{
		{"baseline", "", 1, 1, 0.1, 0.1, false},
		{"only outer duration", "django_http_requests_latency_seconds_by_view_method_count{view=\"crash\",method=\"GET\"} 4\n", 3, 3, 0.5, 0.5, false},
		{"mismatched denominator", "", 4, 3, 0.5, -1, false},
		{"recovery", "", 5, 5, 0.8, 0.8, false},
		{"duration reset", "", 6, 6, 0.7, -1, false},
		{"duplicate duration", djangoDurationSum + " 0.9\n", 7, 7, 0.9, -1, false},
		{"regrown counts after restart", "", 8, 8, 0.2, 0.2, true},
	} {
		t.Run(step.name, func(t *testing.T) {
			body := fmt.Sprintf("%s{status=\"500\"} %d\n%s %d\n%s %g\n%s",
				djangoStatuses, step.requests, djangoDurationCount, step.count, djangoDurationSum, step.sum, step.extra)
			if step.restart {
				body = strings.Replace(body+djangoTestProcess, "process_start_time_seconds 1", "process_start_time_seconds 2", 1)
			}
			setBody(body)
			result := pollDjango(t, c)
			if result.CounterContinuityBreak != (step.name == "baseline" || step.restart) {
				t.Fatalf("unexpected continuity break: %v", result.CounterContinuityBreak)
			}
			assertDjangoSamples(t, result, float64(step.requests), 0, 0, float64(step.requests), step.want)
		})
	}
}

const djangoTestProcess = "process_start_time_seconds 1\nprocess_cpu_seconds_total 1\n"

func djangoTestCollector(t *testing.T, mode string) (*DjangoCollector, func(string)) {
	t.Helper()
	var body atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body.Load().(string))
	}))
	t.Cleanup(server.Close)
	client, err := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewDjangoCollector("app", server.URL, mode, client), func(samples string) {
		if !strings.Contains(samples, "process_start_time_seconds") {
			samples += djangoTestProcess
		}
		body.Store(djangoSignature + " 1\n" + samples)
	}
}

func pollDjango(t *testing.T, c *DjangoCollector) *CollectionResult {
	t.Helper()
	result, err := c.Collect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertDjangoSamples(t *testing.T, result *CollectionResult, requests, notFound, errors4xx, errors5xx, duration float64) {
	t.Helper()
	want := map[string]float64{
		"http_requests_total": requests, "http_404_total": notFound,
		"http_4xx_total": errors4xx, "http_5xx_total": errors5xx,
	}
	if duration >= 0 {
		want["http_request_time_total_seconds"] = duration
	}
	for _, sample := range result.Samples {
		switch sample.Key {
		case "process_uptime", "process_cpu_usage":
			continue
		}
		value, ok := want[sample.Key]
		if !ok || value != sample.Value {
			t.Fatalf("unexpected sample %+v", sample)
		}
		delete(want, sample.Key)
	}
	if len(want) != 0 {
		t.Fatalf("missing samples: %v", want)
	}
}
