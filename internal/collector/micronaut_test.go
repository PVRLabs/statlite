package collector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const micronautTestLabels = `method="GET",status="200",uri="/probe/ok",exception="none"`

func micronautTimer(labels, count, sum string) string {
	var body string
	if count != "" {
		body += "http_server_requests_seconds_count{" + labels + "} " + count + "\n"
	}
	if sum != "" {
		body += "http_server_requests_seconds_sum{" + labels + "} " + sum + "\n"
	}
	return body
}

func newTestMicronautCollector(t *testing.T, handler http.HandlerFunc) *MicronautCollector {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	return NewMicronautCollector("orders", server.URL+"/prometheus", client, nil)
}

func micronautBodyCollector(t *testing.T, body string) *MicronautCollector {
	t.Helper()
	return newTestMicronautCollector(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(body))
	})
}

func collectMicronautBody(t *testing.T, body string) *CollectionResult {
	t.Helper()
	c := micronautBodyCollector(t, body)
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.HealthStatus != "" || result.DBHealthStatus != "" {
		t.Fatalf("fabricated health: %#v", result)
	}
	return result
}

func assertMicronautEvents(t *testing.T, result *CollectionResult, want ...string) {
	t.Helper()
	var got []string
	for _, event := range result.Events {
		got = append(got, event.Type+":"+event.MetricKey)
		if event.Severity != EventSeverityWarning || !strings.Contains(event.Message, "Micronaut") || strings.Contains(event.Message, "Quarkus") {
			t.Fatalf("event = %#v", event)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestMicronautCollectorIdleTrafficRestartShapes(t *testing.T) {
	// Representative shapes from the pinned framework captures: no timers on
	// first idle/restart scrape, four HTTP labels, heap pools and management traffic.
	runtime := func(start, uptime string) string {
		return `process_cpu_usage 0.25
jvm_memory_used_bytes{area="heap",id="G1 Eden Space"} 1000
jvm_memory_used_bytes{area="heap",id="G1 Old Gen"} 2000
jvm_memory_used_bytes{area="heap",id="G1 Survivor Space"} 500
jvm_memory_used_bytes{area="nonheap",id="Metaspace"} 9000
process_start_time_seconds ` + start + "\nprocess_uptime_seconds " + uptime + "\n"
	}
	idle := runtime("1770000000.5", "7.5")
	traffic := runtime("1770000000.5", "90") +
		micronautTimer(micronautTestLabels, "3", "0.25") +
		micronautTimer(`exception="none",method="GET",status="200",uri="/prometheus"`, "3", "0.5") +
		micronautTimer(`exception="NotFoundException",method="GET",status="404",uri="NOT_FOUND"`, "1", "0.125") +
		micronautTimer(`exception="none",method="GET",status="400",uri="/probe/bad"`, "1", "0.0625") +
		micronautTimer(`exception="none",method="GET",status="500",uri="/probe/error"`, "1", "0.0625") +
		`http_server_requests_seconds_bucket{le="+Inf"} 999
http_server_requests_seconds{quantile="0.99"} 999
http_server_requests_seconds_max 999
unrelated_metric{status="500"} 999
`
	bodies := []string{idle, traffic, runtime("1770000100.5", "2")}
	var requests atomic.Int32
	c := newTestMicronautCollector(t, func(w http.ResponseWriter, r *http.Request) {
		n := int(requests.Add(1)) - 1
		if n >= len(bodies) {
			t.Errorf("unexpected extra request")
			http.Error(w, "extra", 500)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(bodies[n]))
	})
	for i := range bodies {
		result, err := c.Collect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if result.TargetName != "orders" || result.PollStartedAt.IsZero() || result.PollFinishedAt.Before(result.PollStartedAt) || result.HealthStatus != "" || result.DBHealthStatus != "" {
			t.Fatalf("poll = %#v", result)
		}
		assertMicronautEvents(t, result)
		expectedStart, expectedUptime := 1770000000.5, 7.5
		if i == 1 {
			expectedUptime = 90
		}
		if i == 2 {
			expectedStart, expectedUptime = 1770000100.5, 2
		}
		want := []MetricSample{}
		if i == 1 {
			want = append(want,
				MetricSample{Key: "http_requests_total", Kind: MetricKindCounter, Value: 9, Unit: "requests"},
				MetricSample{Key: "http_404_total", Kind: MetricKindCounter, Value: 1, Unit: "requests"},
				MetricSample{Key: "http_4xx_total", Kind: MetricKindCounter, Value: 2, Unit: "requests"},
				MetricSample{Key: "http_5xx_total", Kind: MetricKindCounter, Value: 1, Unit: "requests"},
				MetricSample{Key: "http_request_time_total_seconds", Kind: MetricKindCounter, Value: 1, Unit: "seconds"})
		}
		want = append(want,
			MetricSample{Key: "process_cpu_usage", Kind: MetricKindGauge, Value: 0.25, Unit: "ratio"},
			MetricSample{Key: "jvm_heap_used_bytes", Kind: MetricKindGauge, Value: 3500, Unit: "bytes"},
			MetricSample{Key: "process_start_time", Kind: MetricKindGauge, Value: expectedStart, Unit: "unix_seconds"},
			MetricSample{Key: "process_uptime", Kind: MetricKindGauge, Value: expectedUptime, Unit: "seconds"})
		if !reflect.DeepEqual(result.Samples, want) {
			t.Fatalf("poll %d samples = %#v, want %#v", i, result.Samples, want)
		}
		if result.ProcessStartTime == nil || !result.ProcessStartTime.Equal(unixSeconds(expectedStart)) {
			t.Fatalf("start = %v", result.ProcessStartTime)
		}
	}
	if requests.Load() != 3 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestMicronautCollectorStrictHTTPLabels(t *testing.T) {
	var invalid []string
	for _, label := range []string{`method="GET"`, `status="200"`, `uri="/probe/ok"`, `exception="none"`} {
		invalid = append(invalid, strings.Trim(strings.Replace(micronautTestLabels, label, "", 1), ","))
		// Remove the interior empty separator for missing middle labels.
		invalid[len(invalid)-1] = strings.ReplaceAll(invalid[len(invalid)-1], ",,", ",")
		invalid = append(invalid, strings.Replace(micronautTestLabels, label, strings.Split(label, "=")[0]+`=""`, 1))
	}
	for _, status := range []string{"099", "600", "+200", "0200", " 200", "200 ", "200.0", "UNKNOWN", "2０0", "20", "2e2", "-200"} {
		invalid = append(invalid, strings.Replace(micronautTestLabels, `status="200"`, `status="`+status+`"`, 1))
	}
	for _, labels := range invalid {
		t.Run(labels, func(t *testing.T) {
			result := collectMicronautBody(t, "process_cpu_usage 0.1\n"+micronautTimer(micronautTestLabels, "3", "2")+micronautTimer(labels, "9", "9"))
			if got := strings.Join(sampleKeys(result.Samples), ","); got != "http_requests_total,http_request_time_total_seconds,process_cpu_usage" {
				t.Fatalf("keys = %s", got)
			}
			assertSample(t, result, "http_requests_total", MetricKindCounter, 3, "requests")
			assertSample(t, result, "http_request_time_total_seconds", MetricKindCounter, 2, "seconds")
			assertMicronautEvents(t, result, "metric_dimension_invalid:http_requests_total", "metric_dimension_invalid:http_request_time_total_seconds")
		})
	}
	// Any nonempty method, route, or exception is allowed, without rewriting.
	result := collectMicronautBody(t, "process_cpu_usage 0.1\n"+micronautTimer(`method="CUSTOM verb",status="200",uri=" ",exception="custom exception"`, "0.5", "0"))
	assertSample(t, result, "http_requests_total", MetricKindCounter, 0.5, "requests")
	assertSample(t, result, "http_request_time_total_seconds", MetricKindCounter, 0, "seconds")
	assertMicronautEvents(t, result)
}

func TestMicronautCollectorExactStatusFolding(t *testing.T) {
	for _, code := range []int{100, 199, 200, 299, 300, 399, 400, 403, 404, 405, 499, 500, 599} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			labels := strings.Replace(micronautTestLabels, `status="200"`, fmt.Sprintf(`status="%d"`, code), 1)
			result := collectMicronautBody(t, "process_cpu_usage 0\n"+micronautTimer(labels, "2", "0"))
			expected404, expected4xx, expected5xx := 0.0, 0.0, 0.0
			if code == 404 {
				expected404 = 2
			}
			if code >= 400 && code < 500 {
				expected4xx = 2
			}
			if code >= 500 {
				expected5xx = 2
			}
			assertSample(t, result, "http_404_total", MetricKindCounter, expected404, "requests")
			assertSample(t, result, "http_4xx_total", MetricKindCounter, expected4xx, "requests")
			assertSample(t, result, "http_5xx_total", MetricKindCounter, expected5xx, "requests")
			assertMicronautEvents(t, result)
		})
	}
}

func TestMicronautCollectorHTTPIdentityAndFailures(t *testing.T) {
	other := micronautTestLabels + `,extra="other"`
	reordered := `exception="none",uri="/probe/ok",status="200",method="GET"`
	tests := []struct {
		name, body, keys string
		events           []string
	}{
		{"reordered", micronautTimer(micronautTestLabels, "3", "") + micronautTimer(reordered, "", "2"), "http_requests_total,http_404_total,http_4xx_total,http_5xx_total,http_request_time_total_seconds,process_cpu_usage", nil},
		{"extra label mismatch", micronautTimer(micronautTestLabels, "3", "") + micronautTimer(other, "", "2"), "http_requests_total,http_404_total,http_4xx_total,http_5xx_total,process_cpu_usage", []string{"metric_series_mismatch:http_request_time_total_seconds"}},
		{"route mismatch", micronautTimer(micronautTestLabels, "3", "") + micronautTimer(strings.Replace(micronautTestLabels, "/probe/ok", "/other", 1), "", "2"), "http_requests_total,http_404_total,http_4xx_total,http_5xx_total,process_cpu_usage", []string{"metric_series_mismatch:http_request_time_total_seconds"}},
		{"count only", micronautTimer(micronautTestLabels, "3", ""), "http_requests_total,http_404_total,http_4xx_total,http_5xx_total,process_cpu_usage", nil},
		{"sum only", micronautTimer(micronautTestLabels, "", "2"), "process_cpu_usage", []string{"metric_series_mismatch:http_request_time_total_seconds"}},
		{"count duplicate", micronautTimer(micronautTestLabels, "3", "2") + micronautTimer(reordered, "4", ""), "process_cpu_usage", []string{"metric_series_duplicate:http_requests_total"}},
		{"sum duplicate", micronautTimer(micronautTestLabels, "3", "2") + micronautTimer(reordered, "", "4"), "http_requests_total,http_404_total,http_4xx_total,http_5xx_total,process_cpu_usage", []string{"metric_series_duplicate:http_request_time_total_seconds"}},
		{"count overflow keeps duration", micronautTimer(micronautTestLabels, "1e308", "1") + micronautTimer(other, "1e308", "2"), "http_404_total,http_4xx_total,http_5xx_total,http_request_time_total_seconds,process_cpu_usage", []string{"metric_aggregate_invalid:http_requests_total"}},
		{"sum overflow", micronautTimer(micronautTestLabels, "1", "1e308") + micronautTimer(other, "1", "1e308"), "http_requests_total,http_404_total,http_4xx_total,http_5xx_total,process_cpu_usage", []string{"metric_aggregate_invalid:http_request_time_total_seconds"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectMicronautBody(t, "process_cpu_usage 0.1\n"+tt.body)
			if got := strings.Join(sampleKeys(result.Samples), ","); got != tt.keys {
				t.Fatalf("keys = %s, want %s", got, tt.keys)
			}
			assertMicronautEvents(t, result, tt.events...)
			if tt.name == "count overflow keeps duration" {
				assertSample(t, result, "http_request_time_total_seconds", MetricKindCounter, 3, "seconds")
			}
		})
	}
	for _, code := range []string{"404", "499", "599"} {
		t.Run("status overflow "+code, func(t *testing.T) {
			labels := strings.Replace(micronautTestLabels, `status="200"`, `status="`+code+`"`, 1)
			result := collectMicronautBody(t, "process_cpu_usage 0.1\n"+micronautTimer(labels, "1e308", "1")+micronautTimer(labels+`,extra="x"`, "1e308", "2"))
			assertSample(t, result, "http_request_time_total_seconds", MetricKindCounter, 3, "seconds")
			want := []string{"metric_aggregate_invalid:http_requests_total"}
			if code == "404" {
				want = append(want, "metric_aggregate_invalid:http_404_total")
			}
			if code == "404" || code == "499" {
				want = append(want, "metric_aggregate_invalid:http_4xx_total")
			} else {
				want = append(want, "metric_aggregate_invalid:http_5xx_total")
			}
			assertMicronautEvents(t, result, want...)
			for _, event := range result.Events {
				if hasSample(result, event.MetricKey) {
					t.Fatalf("overflowed sample retained: %s", event.MetricKey)
				}
			}
		})
	}
	for _, value := range []string{"-1", "NaN", "+Inf", "-Inf"} {
		t.Run("invalid value "+value, func(t *testing.T) {
			result := collectMicronautBody(t, "process_cpu_usage 0.1\n"+micronautTimer(micronautTestLabels, "3", "2")+micronautTimer(other, value, value))
			assertSample(t, result, "http_requests_total", MetricKindCounter, 3, "requests")
			assertSample(t, result, "http_request_time_total_seconds", MetricKindCounter, 2, "seconds")
			if hasSample(result, "http_404_total") || hasSample(result, "http_4xx_total") || hasSample(result, "http_5xx_total") {
				t.Fatal("invalid count must omit all status counters")
			}
			assertMicronautEvents(t, result, "metric_dimension_invalid:http_requests_total", "metric_dimension_invalid:http_request_time_total_seconds")
		})
	}
}

func TestMicronautCollectorRuntimeCompatibilityAndInspection(t *testing.T) {
	tests := []struct{ name, body, keys, status string }{
		{"cpu zero", "process_cpu_usage 0\n", "process_cpu_usage", "compatible"},
		{"cpu one", "process_cpu_usage 1\n", "process_cpu_usage", "compatible"},
		{"heap zero", `jvm_memory_used_bytes{area="heap"} 0` + "\n", "jvm_heap_used_bytes", "compatible"},
		{"start only", "process_start_time_seconds 1770000000.5\n", "process_start_time", "compatible"},
		{"uptime alone", "process_uptime_seconds 2\n", "", ""},
		{"http alone", micronautTimer(micronautTestLabels, "1", "2"), "", ""},
		{"unrelated", "unrelated 1\nsystem_cpu_usage 0.2\njvm_memory_used_bytes{area=\"nonheap\"} 1\n", "", ""},
		{"empty", "", "", ""},
		{"invalid runtime only", "process_cpu_usage NaN\njvm_memory_used_bytes{area=\"heap\"} -1\nprocess_start_time_seconds 0\nprocess_uptime_seconds 1\n", "", ""},
		{"partial", "process_cpu_usage 1.1\nprocess_start_time_seconds 253402300800\nprocess_uptime_seconds -1\njvm_memory_used_bytes{area=\"heap\"} 100\n", "jvm_heap_used_bytes", "partial"},
		{"duplicate cpu", "process_cpu_usage 0.1\nprocess_cpu_usage 0.2\nprocess_start_time_seconds 1770000000\n", "process_start_time", "partial"},
		{"duplicate start", "process_start_time_seconds 1770000000\nprocess_start_time_seconds 1770000000\nprocess_cpu_usage 0.2\n", "process_cpu_usage", "partial"},
		{"duplicate uptime", "process_uptime_seconds 1\nprocess_uptime_seconds 2\nprocess_cpu_usage 0.2\n", "process_cpu_usage", "partial"},
		{"heap overflow", "jvm_memory_used_bytes{area=\"heap\",id=\"a\"} 1e308\njvm_memory_used_bytes{area=\"heap\",id=\"b\"} 1e308\nprocess_cpu_usage 0.2\n", "process_cpu_usage", "partial"},
		{"invalid heap pool", "jvm_memory_used_bytes{area=\"heap\",id=\"a\"} 2\njvm_memory_used_bytes{area=\"heap\",id=\"b\"} NaN\nprocess_cpu_usage 0.2\n", "process_cpu_usage", "partial"},
		{"partial HTTP", "process_cpu_usage 0.1\n" + micronautTimer(`method="GET",status="200"`, "1", "1"), "process_cpu_usage", "partial"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := micronautBodyCollector(t, tt.body)
			result, err := c.Collect(context.Background())
			inspection, inspectErr := InspectMicronaut(context.Background(), c.endpoint, c.client)
			if tt.status == "" {
				if !errors.Is(err, ErrMicronautIncompatible) || !errors.Is(inspectErr, ErrMicronautIncompatible) || inspection != nil {
					t.Fatalf("errors = %v / %v, inspection = %#v", err, inspectErr, inspection)
				}
				if len(result.Samples) != 0 || result.ProcessStartTime != nil || len(result.Events) != 1 || result.Events[0].Type != "metrics_source_incompatible" {
					t.Fatalf("result = %#v", result)
				}
				return
			}
			if err != nil || inspectErr != nil {
				t.Fatalf("errors = %v / %v", err, inspectErr)
			}
			if got := strings.Join(sampleKeys(result.Samples), ","); got != tt.keys {
				t.Fatalf("keys = %s, want %s", got, tt.keys)
			}
			if inspection.Status != tt.status || !reflect.DeepEqual(inspection.Capabilities, sampleKeys(result.Samples)) || len(inspection.Warnings) != len(result.Events) {
				t.Fatalf("inspection = %#v, result = %#v", inspection, result)
			}
			for i, event := range result.Events {
				if inspection.Warnings[i] != event.Message {
					t.Fatalf("inspection and runtime warnings differ")
				}
			}
			if tt.name == "partial" {
				assertMicronautEvents(t, result, "metrics_partial:")
				if result.Events[0].Message != "Micronaut metrics are partial; invalid concepts were omitted: process CPU, process start time, process uptime" {
					t.Fatalf("warning = %s", result.Events[0].Message)
				}
			}
		})
	}
	// Invalid optional runtime values preserve an independent compatible concept.
	for _, metric := range []string{"process_cpu_usage", "process_start_time_seconds", "process_uptime_seconds", "jvm_memory_used_bytes{area=\"heap\"}"} {
		for _, value := range []string{"-1", "NaN", "+Inf", "-Inf"} {
			t.Run(metric+value, func(t *testing.T) {
				support := "process_cpu_usage 0.1\n"
				if metric == "process_cpu_usage" {
					support = "jvm_memory_used_bytes{area=\"heap\"} 1\n"
				}
				result := collectMicronautBody(t, support+metric+" "+value+"\n")
				if len(result.Samples) != 1 {
					t.Fatalf("samples = %#v", result.Samples)
				}
				assertMicronautEvents(t, result, "metrics_partial:")
			})
		}
	}
}

func TestMicronautCollectorScrapeFailuresDiscardTentativeSamples(t *testing.T) {
	tests := []struct {
		name, body string
		status     int
	}{
		{"duplicate label", "process_cpu_usage 0.1\n" + micronautTimer(micronautTestLabels+`,method="POST"`, "1", ""), 200},
		{"malformed labels", "process_cpu_usage 0.1\nbroken{method=\"unterminated} 1\n", 200},
		{"malformed value", "process_cpu_usage 0.1\nbroken nope\n", 200},
		{"unauthorized", "process_cpu_usage 0.1\n", 401},
		{"missing", "process_cpu_usage 0.1\n", 404},
		{"oversized", "process_cpu_usage 0.1\n#" + strings.Repeat("x", 4*1024*1024) + "\n", 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			c := newTestMicronautCollector(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "text/plain; version=0.0.4")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			})
			result, err := c.Collect(context.Background())
			if err == nil || errors.Is(err, ErrMicronautIncompatible) || len(result.Samples) != 0 || result.ProcessStartTime != nil || len(result.Events) != 1 || result.Events[0].Type != "metrics_fetch_failed" {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
			if requests.Load() != 1 || result.HealthStatus != "" || result.DBHealthStatus != "" {
				t.Fatalf("requests = %d, result = %#v", requests.Load(), result)
			}
			inspection, err := InspectMicronaut(context.Background(), c.endpoint, c.client)
			if err == nil || inspection != nil {
				t.Fatalf("inspection = %#v, error = %v", inspection, err)
			}
		})
	}
}

func TestMicronautCollectorExactEndpointAuthAndOpenMetrics(t *testing.T) {
	var requests atomic.Int32
	c := newTestMicronautCollector(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.RequestURI() != "/svc%2Fwest/custom/?scope=app" {
			t.Errorf("URI = %s", r.URL.RequestURI())
		}
		user, password, ok := r.BasicAuth()
		if !ok || user != "reader" || password != "secret" {
			t.Errorf("missing expected basic auth")
		}
		w.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0")
		_, _ = w.Write([]byte("process_cpu_usage 0.1\n" + micronautTimer(micronautTestLabels, "1", "2") + "# EOF\n"))
	})
	c.endpoint = strings.TrimSuffix(c.endpoint, "/prometheus") + "/svc%2Fwest/custom/?scope=app"
	client, err := prometheus.NewClient(time.Second, prometheus.DefaultLimits, &prometheus.BasicAuth{Username: "reader", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	c.client = client
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertSample(t, result, "http_requests_total", MetricKindCounter, 1, "requests")
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
	assertMicronautEvents(t, result)
}

func TestMicronautCollectorNotConfigured(t *testing.T) {
	client, err := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []*MicronautCollector{NewMicronautCollector("orders", "http://localhost/prometheus", nil, nil), NewMicronautCollector("orders", "", client, nil)} {
		result, err := c.Collect(context.Background())
		if err == nil || len(result.Events) != 1 || result.Events[0].Type != "collector_not_configured" || result.PollFinishedAt.IsZero() {
			t.Fatalf("result = %#v, error = %v", result, err)
		}
		inspection, err := InspectMicronaut(context.Background(), c.endpoint, c.client)
		if err == nil || inspection != nil {
			t.Fatalf("inspection = %#v, error = %v", inspection, err)
		}
	}
}

func TestMicronautCollectorCombinedMatchingBound(t *testing.T) {
	var body strings.Builder
	body.WriteString("process_cpu_usage 0.1\n")
	for i := 0; i < micrometerHTTPMatchingStateLimit/2; i++ {
		body.WriteString(micronautTimer(micronautTestLabels+fmt.Sprintf(`,extra="%d"`, i), "1", "0.5"))
	}
	exact := collectMicronautBody(t, body.String())
	assertSample(t, exact, "http_requests_total", MetricKindCounter, 10000, "requests")
	assertSample(t, exact, "http_request_time_total_seconds", MetricKindCounter, 5000, "seconds")
	assertMicronautEvents(t, exact)
	body.WriteString(micronautTimer(micronautTestLabels+`,extra="overflow"`, "1", "0.5"))
	overflow := collectMicronautBody(t, body.String())
	if strings.Join(sampleKeys(overflow.Samples), ",") != "process_cpu_usage" {
		t.Fatalf("samples = %#v", overflow.Samples)
	}
	assertMicronautEvents(t, overflow, "metric_aggregation_limit:")
}
