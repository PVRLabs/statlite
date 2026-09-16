package collector

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const validGoMetrics = `# TYPE go_http_request_duration_seconds histogram
go_http_request_duration_seconds_bucket{code="200",method="get",le="1"} 4
go_http_request_duration_seconds_count{code="200",method="get"} 4
go_http_request_duration_seconds_sum{method="get",code="200"} 1.5
go_http_request_duration_seconds_count{method="post",code="404"} 2
go_http_request_duration_seconds_sum{code="404",method="post"} 0.5
go_http_request_duration_seconds_count{method="unknown",code="500"} 1
go_http_request_duration_seconds_sum{method="unknown",code="500"} 0.25
# TYPE go_memstats_heap_alloc_bytes gauge
go_memstats_heap_alloc_bytes 1024
# TYPE process_start_time_seconds gauge
process_start_time_seconds 1770000000
go_http_requests_total{code="200",method="get"} 999
unrelated_metric{dimension="ignored"} 12
`

func TestGoMetricsNormalizerAcceptsExactContract(t *testing.T) {
	evaluation, err := parseGoMetrics(t, validGoMetrics, prometheus.DefaultLimits, time.Unix(1770000060, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	assertGoSamples(t, evaluation.samples, map[string]float64{
		"http_requests_total": 7, "http_404_total": 2, "http_4xx_total": 2,
		"http_5xx_total": 1, "http_request_time_total_seconds": 2.25,
		"runtime_heap_used_bytes": 1024, "process_start_time": 1770000000, "process_uptime": 60,
	})
	if len(evaluation.httpPopulation) != 3 || evaluation.processStartTime == nil {
		t.Fatalf("evaluation = %#v, want three tuples and process identity", evaluation)
	}
}

func TestGoMetricsNormalizerRejectsInvalidRequiredContract(t *testing.T) {
	validOne := "# TYPE go_http_request_duration_seconds histogram\n" +
		"go_http_request_duration_seconds_count{code=\"200\",method=\"get\"} 1\n" +
		"go_http_request_duration_seconds_sum{code=\"200\",method=\"get\"} 0.1\n"
	tests := map[string]string{
		"missing type":      strings.Replace(validOne, "# TYPE go_http_request_duration_seconds histogram\n", "", 1),
		"wrong type":        strings.Replace(validOne, "histogram", "summary", 1),
		"late type":         strings.TrimPrefix(validOne, "# TYPE go_http_request_duration_seconds histogram\n") + "# TYPE go_http_request_duration_seconds histogram\n",
		"duplicate type":    "# TYPE go_http_request_duration_seconds histogram\n" + validOne,
		"extra label":       strings.Replace(validOne, "method=\"get\"", "method=\"get\",route=\"/\"", 1),
		"invalid method":    strings.ReplaceAll(validOne, "method=\"get\"", "method=\"GET\""),
		"invalid status":    strings.ReplaceAll(validOne, "code=\"200\"", "code=\"600\""),
		"fractional count":  strings.Replace(validOne, "} 1\n", "} 1.5\n", 1),
		"oversized count":   strings.Replace(validOne, "} 1\n", "} 9007199254740992\n", 1),
		"negative sum":      strings.Replace(validOne, "} 0.1\n", "} -0.1\n", 1),
		"nan sum":           strings.Replace(validOne, "} 0.1\n", "} NaN\n", 1),
		"duplicate count":   validOne + "go_http_request_duration_seconds_count{code=\"200\",method=\"get\"} 1\n",
		"tuple mismatch":    strings.Replace(validOne, "_sum{code=\"200\"", "_sum{code=\"201\"", 1),
		"conflicting alias": validOne + "go_http_request_duration_seconds_total 1\n",
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			evaluation, err := parseGoMetrics(t, input, prometheus.DefaultLimits, time.Now())
			if !errors.Is(err, errGoMetricsContract) || evaluation != nil {
				t.Fatalf("evaluation, err = %#v, %v; want no partial output and contract error", evaluation, err)
			}
		})
	}
}

func TestGoMetricsNormalizerRejectsBoundsAndAggregationOverflow(t *testing.T) {
	input := "# TYPE go_http_request_duration_seconds histogram\n" +
		"go_http_request_duration_seconds_count{code=\"200\",method=\"get\"} 1\n" +
		"go_http_request_duration_seconds_sum{code=\"200\",method=\"get\"} 1\n" +
		"go_http_request_duration_seconds_count{code=\"201\",method=\"post\"} 1\n" +
		"go_http_request_duration_seconds_sum{code=\"201\",method=\"post\"} 1\n"
	limits := prometheus.DefaultLimits
	limits.MaxAggregationStates = 1
	if evaluation, err := parseGoMetrics(t, input, limits, time.Now()); !errors.Is(err, errGoMetricsContract) || evaluation != nil {
		t.Fatalf("state-limit result = %#v, %v", evaluation, err)
	}
	overflow := strings.Replace(input, "} 1\n", fmt.Sprintf("} %.0f\n", maxExactCount), 1)
	if evaluation, err := parseGoMetrics(t, overflow, prometheus.DefaultLimits, time.Now()); !errors.Is(err, errGoMetricsContract) || evaluation != nil {
		t.Fatalf("overflow result = %#v, %v", evaluation, err)
	}
	durationOverflow := strings.Replace(input, "_sum{code=\"200\",method=\"get\"} 1", "_sum{code=\"200\",method=\"get\"} 1e308", 1)
	durationOverflow = strings.Replace(durationOverflow, "_sum{code=\"201\",method=\"post\"} 1", "_sum{code=\"201\",method=\"post\"} 1e308", 1)
	if evaluation, err := parseGoMetrics(t, durationOverflow, prometheus.DefaultLimits, time.Now()); !errors.Is(err, errGoMetricsContract) || evaluation != nil {
		t.Fatalf("duration-overflow result = %#v, %v", evaluation, err)
	}
}

func TestGoMetricsNormalizerValidatesOptionalFamiliesIndependently(t *testing.T) {
	input := strings.Replace(validGoMetrics, "# TYPE go_memstats_heap_alloc_bytes gauge", "# TYPE go_memstats_heap_alloc_bytes counter", 1)
	input = strings.Replace(input, "process_start_time_seconds 1770000000", "process_start_time_seconds 1999999999", 1)
	evaluation, err := parseGoMetrics(t, input, prometheus.DefaultLimits, time.Unix(1770000060, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	if hasSampleIn(evaluation.samples, "runtime_heap_used_bytes") || hasSampleIn(evaluation.samples, "process_start_time") || hasSampleIn(evaluation.samples, "process_uptime") {
		t.Fatalf("invalid optional concepts were normalized: %#v", evaluation.samples)
	}
	assertSampleValue(t, evaluation.samples, "http_requests_total", 7)
}

func TestGoMetricsEvaluationPropagatesUnrelatedParserFailureWithoutOutput(t *testing.T) {
	for name, suffix := range map[string]string{
		"syntax":       "unrelated{bad=\"unterminated} 1\n",
		"sample limit": "extra_one 1\nextra_two 2\n",
	} {
		t.Run(name, func(t *testing.T) {
			body := "# TYPE go_http_request_duration_seconds histogram\n" +
				"go_http_request_duration_seconds_count{code=\"200\",method=\"get\"} 1\n" +
				"go_http_request_duration_seconds_sum{code=\"200\",method=\"get\"} 0.1\n" + suffix
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain; version=0.0.4")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			limits := prometheus.DefaultLimits
			if name == "sample limit" {
				limits.MaxSamples = 3
			}
			client, err := prometheus.NewClient(time.Second, limits, nil)
			if err != nil {
				t.Fatal(err)
			}
			evaluation, err := evaluateGoMetrics(context.Background(), server.URL, client, time.Now())
			if err == nil || evaluation != nil {
				t.Fatalf("evaluation, err = %#v, %v; want parser failure and no output", evaluation, err)
			}
		})
	}
}

func parseGoMetrics(t *testing.T, input string, limits prometheus.Limits, now time.Time) (*goMetricsEvaluation, error) {
	t.Helper()
	n := newGoMetricsNormalizer(limits.MaxAggregationStates)
	if _, err := prometheus.ParseWithMetadata(strings.NewReader(input), prometheus.TextFormat, limits, n.acceptMetadata, n.acceptSample); err != nil {
		return nil, err
	}
	return n.finish(now)
}

func assertGoSamples(t *testing.T, samples []MetricSample, want map[string]float64) {
	t.Helper()
	if len(samples) != len(want) {
		t.Fatalf("samples = %#v, want %d samples", samples, len(want))
	}
	for key, value := range want {
		assertSampleValue(t, samples, key, value)
	}
}

func assertSampleValue(t *testing.T, samples []MetricSample, key string, want float64) {
	t.Helper()
	for _, sample := range samples {
		if sample.Key == key {
			if math.Abs(sample.Value-want) > 1e-9 {
				t.Fatalf("%s = %v, want %v", key, sample.Value, want)
			}
			return
		}
	}
	t.Fatalf("missing sample %s in %#v", key, samples)
}

func hasSampleIn(samples []MetricSample, key string) bool {
	for _, sample := range samples {
		if sample.Key == key {
			return true
		}
	}
	return false
}
