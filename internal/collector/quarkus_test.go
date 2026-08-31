package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

func TestQuarkusCollectorUsesOneLogicalScrapeAndPreservesExactEndpoint(t *testing.T) {
	var requests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/q/metrics/", func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.RawQuery != "scope=app" {
			t.Errorf("query = %q, want scope=app", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("process_cpu_usage 0.25\n"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	c := newTestQuarkusCollector(t, server.URL+"/q/metrics/?scope=app", nil, prometheus.DefaultLimits)
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("wire requests = %d, want one logical scrape with one request", requests.Load())
	}
	if result.HealthStatus != "" || result.DBHealthStatus != "" || len(result.Samples) != 0 {
		t.Fatalf("result = %#v, want shell result without health or normalized samples", result)
	}
}

func TestQuarkusCollectorNormalizesCertifiedHTTPFamilies(t *testing.T) {
	body := `
# TYPE http_server_requests_seconds summary
http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200",uri="/ok",exception="none"} 7
http_server_requests_seconds_count{method="GET",outcome="CLIENT_ERROR",status="404",uri="/missing"} 2
http_server_requests_seconds_count{method="POST",outcome="CLIENT_ERROR",status="418",uri="/teapot"} 3
http_server_requests_seconds_count{method="GET",outcome="SERVER_ERROR",status="500",uri="/error"} 4
http_server_requests_seconds_sum{method="GET",outcome="SUCCESS",status="200",uri="/ok",exception="none"} 1.25 # {trace_id="abc"} 0.01
http_server_requests_seconds_sum{method="GET",outcome="CLIENT_ERROR",status="404",uri="/missing"} 0.5
http_server_requests_seconds_sum{method="POST",outcome="CLIENT_ERROR",status="418",uri="/teapot"} 0.25
http_server_requests_seconds_sum{method="GET",outcome="SERVER_ERROR",status="500",uri="/error"} 0.75
http_server_requests_seconds_bucket{method="GET",outcome="SUCCESS",status="200",le="+Inf"} 999
http_server_requests_count{method="GET",outcome="SUCCESS",status="200"} 999
unknown_metric{status="500"} 999
process_cpu_usage 0.25
# EOF
`
	result := collectQuarkusOpenMetrics(t, body)

	assertQuarkusSamples(t, result.Samples, []MetricSample{
		{Key: "http_requests_total", Kind: MetricKindCounter, Value: 16, Unit: "requests"},
		{Key: "http_404_total", Kind: MetricKindCounter, Value: 2, Unit: "requests"},
		{Key: "http_4xx_total", Kind: MetricKindCounter, Value: 5, Unit: "requests"},
		{Key: "http_5xx_total", Kind: MetricKindCounter, Value: 4, Unit: "requests"},
		{Key: "http_request_time_total_seconds", Kind: MetricKindCounter, Value: 2.75, Unit: "seconds"},
	})
	if len(result.Events) != 0 {
		t.Fatalf("events = %#v, want none", result.Events)
	}
}

func TestQuarkusCollectorHTTPStatusZeroRequiresCompleteAcceptedDimensions(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantKeys   []string
		wantEvents []CollectorEvent
	}{
		{
			name: "accepted status proves genuine zeros",
			body: `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200"} 3
process_cpu_usage 0.1
`,
			wantKeys: []string{"http_requests_total", "http_404_total", "http_4xx_total", "http_5xx_total"},
		},
		{
			name: "missing status omits status concepts",
			body: `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200"} 3
http_server_requests_seconds_count{method="GET",outcome="SUCCESS"} 2
process_cpu_usage 0.1
`,
			wantKeys:   []string{"http_requests_total"},
			wantEvents: []CollectorEvent{{Severity: EventSeverityWarning, Type: "metric_dimension_invalid", MetricKey: "http_requests_total", Message: "ignored http_server_requests_seconds_count series without valid method, outcome, and status dimensions and a finite nonnegative value; status counters are unavailable"}},
		},
		{
			name: "non-finite error count cannot prove zero",
			body: `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200"} 3
http_server_requests_seconds_count{method="GET",outcome="SERVER_ERROR",status="500"} NaN
process_cpu_usage 0.1
`,
			wantKeys:   []string{"http_requests_total"},
			wantEvents: []CollectorEvent{{Severity: EventSeverityWarning, Type: "metric_dimension_invalid", MetricKey: "http_requests_total", Message: "ignored http_server_requests_seconds_count series without valid method, outcome, and status dimensions and a finite nonnegative value; status counters are unavailable"}},
		},
		{
			name: "invalid status and missing required labels are rejected",
			body: `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="unknown"} 2
http_server_requests_seconds_sum{method="GET",status="200"} 9
process_cpu_usage 0.1
`,
			wantEvents: []CollectorEvent{
				{Severity: EventSeverityWarning, Type: "metric_dimension_invalid", MetricKey: "http_requests_total", Message: "ignored http_server_requests_seconds_count series without valid method, outcome, and status dimensions and a finite nonnegative value; status counters are unavailable"},
				{Severity: EventSeverityWarning, Type: "metric_dimension_invalid", MetricKey: "http_request_time_total_seconds", Message: "ignored http_server_requests_seconds_sum series without valid method, outcome, and status dimensions and a finite nonnegative value"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectQuarkusBody(t, tt.body)
			if got := sampleKeys(result.Samples); strings.Join(got, ",") != strings.Join(tt.wantKeys, ",") {
				t.Fatalf("sample keys = %v, want %v", got, tt.wantKeys)
			}
			if len(result.Events) != len(tt.wantEvents) {
				t.Fatalf("events = %#v, want %#v", result.Events, tt.wantEvents)
			}
			for i := range tt.wantEvents {
				if result.Events[i] != tt.wantEvents[i] {
					t.Fatalf("event[%d] = %#v, want %#v", i, result.Events[i], tt.wantEvents[i])
				}
			}
		})
	}
}

func TestQuarkusCollectorOmitsDurationUnlessItsDimensionsMatchCounts(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "duration without count",
			body: `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200"} 3
http_server_requests_seconds_sum{method="GET",outcome="SERVER_ERROR",status="500"} 2
process_cpu_usage 0.1
`,
		},
		{
			name: "count without duration",
			body: `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200"} 3
http_server_requests_seconds_count{method="GET",outcome="SERVER_ERROR",status="500"} 1
http_server_requests_seconds_sum{method="GET",outcome="SUCCESS",status="200"} 2
process_cpu_usage 0.1
`,
		},
		{
			name: "different URI series",
			body: `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200",uri="/a"} 3
http_server_requests_seconds_sum{method="GET",outcome="SUCCESS",status="200",uri="/b"} 2
process_cpu_usage 0.1
`,
		},
		{
			name: "rejected count cannot match duration",
			body: `http_server_requests_seconds_count{method="GET",outcome="SERVER_ERROR",status="500"} NaN
http_server_requests_seconds_sum{method="GET",outcome="SERVER_ERROR",status="500"} 2
process_cpu_usage 0.1
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectQuarkusBody(t, tt.body)
			for _, sample := range result.Samples {
				if sample.Key == "http_request_time_total_seconds" {
					t.Fatalf("unexpected unmatched duration sample: %#v", sample)
				}
			}
			if !hasQuarkusCollectorEvent(result.Events, "metric_series_mismatch", "http_request_time_total_seconds") {
				t.Fatalf("events = %#v, want duration mismatch warning", result.Events)
			}
		})
	}
}

func TestQuarkusCollectorMatchesFullSeriesIdentityIndependentOfLabelOrder(t *testing.T) {
	body := `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200",uri="/a",exception="none"} 3
http_server_requests_seconds_sum{exception="none",uri="/a",status="200",outcome="SUCCESS",method="GET"} 2
process_cpu_usage 0.1
`
	result := collectQuarkusBody(t, body)
	for _, sample := range result.Samples {
		if sample.Key == "http_request_time_total_seconds" && sample.Value == 2 {
			return
		}
	}
	t.Fatalf("samples = %#v, want matched duration despite label order", result.Samples)
}

func TestQuarkusCollectorOmitsAggregatesThatOverflow(t *testing.T) {
	body := `http_server_requests_seconds_count{method="GET",outcome="SERVER_ERROR",status="500",uri="/a"} 1e308
http_server_requests_seconds_count{method="GET",outcome="SERVER_ERROR",status="500",uri="/b"} 1e308
http_server_requests_seconds_sum{method="GET",outcome="SERVER_ERROR",status="500",uri="/a"} 1e308
http_server_requests_seconds_sum{method="GET",outcome="SERVER_ERROR",status="500",uri="/b"} 1e308
process_cpu_usage 0.1
`
	result := collectQuarkusBody(t, body)
	if len(result.Samples) != 0 {
		t.Fatalf("samples = %#v, want no non-finite aggregates", result.Samples)
	}
	for _, key := range []string{"http_requests_total", "", "http_request_time_total_seconds"} {
		if !hasQuarkusCollectorEvent(result.Events, "metric_aggregate_invalid", key) {
			t.Fatalf("events = %#v, want overflow diagnostic for %s", result.Events, key)
		}
	}
}

func TestQuarkusCollectorRejectsRepeatedSeriesIdentities(t *testing.T) {
	body := `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200",uri="/a"} 3
http_server_requests_seconds_count{uri="/a",status="200",outcome="SUCCESS",method="GET"} 3
http_server_requests_seconds_sum{method="GET",outcome="SUCCESS",status="200",uri="/a"} 2
http_server_requests_seconds_sum{uri="/a",status="200",outcome="SUCCESS",method="GET"} 2
process_cpu_usage 0.1
`
	result := collectQuarkusBody(t, body)
	if len(result.Samples) != 0 {
		t.Fatalf("samples = %#v, want duplicated HTTP families omitted", result.Samples)
	}
	for _, key := range []string{"http_requests_total", "http_request_time_total_seconds"} {
		if !hasQuarkusCollectorEvent(result.Events, "metric_series_duplicate", key) {
			t.Fatalf("events = %#v, want duplicate diagnostic for %s", result.Events, key)
		}
	}
}

func TestQuarkusHTTPDurationMatchingStateIsBounded(t *testing.T) {
	v := quarkusHTTPValues{completeCountLabels: true}
	for i := 0; i < quarkusHTTPMatchingStateLimit+100; i++ {
		v.acceptCount(prometheus.Sample{
			Name:   "http_server_requests_seconds_count",
			Value:  1,
			Labels: []prometheus.Label{{Name: "method", Value: "METHOD_" + strconv.Itoa(i)}, {Name: "outcome", Value: "SUCCESS"}, {Name: "status", Value: "200"}},
		})
	}
	if !v.matchingOverflow || v.matchingStates != quarkusHTTPMatchingStateLimit || len(v.countDimensions) != quarkusHTTPMatchingStateLimit {
		t.Fatalf("matching state = overflow:%v states:%d groups:%d", v.matchingOverflow, v.matchingStates, len(v.countDimensions))
	}
}

func TestQuarkusCollectorHTTPAggregationStateAndOutputStayBounded(t *testing.T) {
	var body strings.Builder
	for i := 0; i < 10_000; i++ {
		body.WriteString(`http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200",uri="/item/`)
		body.WriteString(strconv.Itoa(i))
		body.WriteString(`"} 1` + "\n")
	}
	body.WriteString("process_cpu_usage 0.1\n")

	result := collectQuarkusBody(t, body.String())
	assertQuarkusSamples(t, result.Samples, []MetricSample{
		{Key: "http_requests_total", Kind: MetricKindCounter, Value: 10_000, Unit: "requests"},
		{Key: "http_404_total", Kind: MetricKindCounter, Value: 0, Unit: "requests"},
		{Key: "http_4xx_total", Kind: MetricKindCounter, Value: 0, Unit: "requests"},
		{Key: "http_5xx_total", Kind: MetricKindCounter, Value: 0, Unit: "requests"},
	})
	if len(result.Samples) != 4 || len(result.Events) != 0 {
		t.Fatalf("result cardinality = %d samples, %d events; want 4 and 0", len(result.Samples), len(result.Events))
	}
}

func TestQuarkusCollectorRedirectsRemainOneLogicalScrapeAndAreBounded(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/metrics", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("process_start_time_seconds 1\n"))
	}))
	defer server.Close()

	result, err := newTestQuarkusCollector(t, server.URL+"/start", nil, prometheus.DefaultLimits).Collect(context.Background())
	if err != nil || len(result.Events) != 0 || requests.Load() != 2 {
		t.Fatalf("Collect() = (%#v, %v), requests = %d; want one successful scrape over redirect", result, err, requests.Load())
	}

	requests.Store(0)
	limits := prometheus.DefaultLimits
	limits.MaxRedirects = 0
	_, err = newTestQuarkusCollector(t, server.URL+"/start", nil, limits).Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redirect limit exceeded") || requests.Load() != 1 {
		t.Fatalf("Collect() error = %v, requests = %d; want bounded redirect failure", err, requests.Load())
	}
}

func TestQuarkusCollectorUsesBasicAuthAndRejectsIncompatibleScrape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "user" || password != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("unrelated_metric 1\n"))
	}))
	defer server.Close()

	auth := &prometheus.BasicAuth{Username: "user", Password: "secret"}
	result, err := newTestQuarkusCollector(t, server.URL, auth, prometheus.DefaultLimits).Collect(context.Background())
	if err == nil || !strings.Contains(err.Error(), "does not expose") {
		t.Fatalf("Collect() error = %v, want incompatible error", err)
	}
	if len(result.Events) != 1 || result.Events[0].Type != "metrics_source_incompatible" || result.HealthStatus != "" {
		t.Fatalf("result = %#v, want focused incompatible event without health", result)
	}
}

func newTestQuarkusCollector(t *testing.T, endpoint string, auth *prometheus.BasicAuth, limits prometheus.Limits) *QuarkusCollector {
	t.Helper()
	client, err := prometheus.NewClient(time.Second, limits, auth)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return NewQuarkusCollector("orders", endpoint, client)
}

func collectQuarkusBody(t *testing.T, body string) *CollectionResult {
	return collectQuarkusContent(t, "text/plain; version=0.0.4", body)
}

func collectQuarkusOpenMetrics(t *testing.T, body string) *CollectionResult {
	return collectQuarkusContent(t, "application/openmetrics-text; version=1.0.0", body)
}

func collectQuarkusContent(t *testing.T, contentType, body string) *CollectionResult {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	result, err := newTestQuarkusCollector(t, server.URL, nil, prometheus.DefaultLimits).Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	return result
}

func assertQuarkusSamples(t *testing.T, got, want []MetricSample) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("samples = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sample[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func sampleKeys(samples []MetricSample) []string {
	keys := make([]string, 0, len(samples))
	for _, sample := range samples {
		keys = append(keys, sample.Key)
	}
	return keys
}

func hasQuarkusCollectorEvent(events []CollectorEvent, eventType, metricKey string) bool {
	for _, event := range events {
		if event.Type == eventType && event.MetricKey == metricKey {
			return true
		}
	}
	return false
}
