package collector

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

func TestGoCollectorLifecycleAndBaselineBreaks(t *testing.T) {
	body := goCollectorBody(1000, [][4]any{{"get", "200", 10, 1.0}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()
	client, err := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := NewGoCollector("app", server.URL, client)

	assertGoCollect(t, c, false, 0, 0) // first observation is only a raw baseline
	assertGoCollect(t, c, true, 0, 0)  // steady idle keeps a stable logical counter

	body = goCollectorBody(1000, [][4]any{{"get", "200", 12, 1.2}, {"post", "404", 1, 0.4}})
	result := assertGoCollect(t, c, true, 3, 0.6)
	assertGoSampleValue(t, result, "http_404_total", 1)

	// A disappearing tuple breaks continuity even while the aggregate grows.
	body = goCollectorBody(1000, [][4]any{{"get", "200", 20, 2.0}})
	result = assertGoCollect(t, c, false, 0, 0)
	if !hasCollectorEvent(result, "http_continuity_broken", "http_requests_total") {
		t.Fatalf("disappearance events = %#v, want continuity warning", result.Events)
	}
	body = goCollectorBody(1000, [][4]any{{"get", "200", 21, 2.1}})
	assertGoCollect(t, c, true, 4, 0.7)

	// Complete withdrawal is a visible failure. Recovery establishes a baseline
	// and the following observation resumes both count and duration together.
	body = "# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes 2048\n"
	result, err = c.Collect(context.Background())
	if !errorsIsGoContract(err) || hasSample(result, "http_requests_total") || !hasSample(result, "runtime_heap_used_bytes") {
		t.Fatalf("withdrawal result=%#v err=%v", result, err)
	}
	body = goCollectorBody(1000, [][4]any{{"get", "200", 30, 3.0}})
	assertGoCollect(t, c, false, 0, 0)
	body = goCollectorBody(1000, [][4]any{{"get", "200", 32, 3.5}})
	assertGoCollect(t, c, true, 6, 1.2)

	// A per-tuple reset is not promoted to a restart and also needs a baseline.
	body = goCollectorBody(1000, [][4]any{{"get", "200", 1, 0.1}})
	assertGoCollect(t, c, false, 0, 0)
	body = goCollectorBody(1000, [][4]any{{"get", "200", 2, 0.2}})
	assertGoCollect(t, c, true, 7, 1.3)
}

func TestGoCollectorProcessRunAndFailedScrapeHandling(t *testing.T) {
	body := goCollectorBody(1000, [][4]any{{"get", "200", 10, 1.0}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()
	client, _ := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	c := NewGoCollector("app", server.URL, client)
	assertGoCollect(t, c, false, 0, 0)
	body = goCollectorBody(1000, [][4]any{{"get", "200", 11, 1.1}})
	assertGoCollect(t, c, true, 1, 0.1)

	// Malformed input is a failed scrape, not evidence of a new run.
	body = "malformed{"
	if result, err := c.Collect(context.Background()); err == nil || hasSample(result, "http_requests_total") {
		t.Fatalf("malformed result=%#v err=%v", result, err)
	}
	body = goCollectorBody(1000, [][4]any{{"get", "200", 20, 2.0}})
	assertGoCollect(t, c, false, 0, 0)
	body = goCollectorBody(1000, [][4]any{{"get", "200", 21, 2.1}})
	assertGoCollect(t, c, true, 2, 0.2)

	// A distinct authoritative start time resets run history and logical totals.
	body = goCollectorBody(1001, [][4]any{{"post", "201", 1, 0.2}})
	assertGoCollect(t, c, false, 0, 0)
	body = goCollectorBody(1001, [][4]any{{"post", "201", 2, 0.3}, {"get", "200", 1, 0.1}})
	assertGoCollect(t, c, true, 2, 0.2)

	// Same-second identity cannot prove a restart; decreases only break continuity.
	body = goCollectorBody(1001, [][4]any{{"post", "201", 0, 0.0}})
	assertGoCollect(t, c, false, 0, 0)
	body = goCollectorBody(1001, [][4]any{{"post", "201", 1, 0.1}})
	assertGoCollect(t, c, true, 3, 0.3)
}

func TestGoCollectorFreshUnobservedHTTPIsQuietButBucketOnlyIsIncompatible(t *testing.T) {
	for _, test := range []struct {
		name     string
		body     string
		wantHeap bool
	}{
		{name: "runtime only", body: "# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes 2048\n", wantHeap: true},
		{name: "HTTP TYPE only", body: "# TYPE go_http_request_duration_seconds histogram\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain; version=0.0.4")
				_, _ = fmt.Fprint(w, test.body)
			}))
			defer server.Close()
			client, _ := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
			c := NewGoCollector("app", server.URL, client)
			for range 2 {
				result, err := c.Collect(context.Background())
				if err != nil || len(result.Events) != 0 || hasSample(result, "http_requests_total") || hasSample(result, "runtime_heap_used_bytes") != test.wantHeap {
					t.Fatalf("unobserved result=%#v err=%v", result, err)
				}
			}
		})
	}

	bucketOnly := "# TYPE go_http_request_duration_seconds histogram\n" +
		"go_http_request_duration_seconds_bucket{method=\"get\",code=\"200\",le=\"1\"} 1\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprint(w, bucketOnly)
	}))
	defer server.Close()
	client, _ := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	result, err := NewGoCollector("app", server.URL, client).Collect(context.Background())
	if !errors.Is(err, errGoMetricsContract) || !hasCollectorEvent(result, "metrics_source_incompatible", "") {
		t.Fatalf("bucket-only result=%#v err=%v", result, err)
	}
}

func TestGoCollectorLogicalCounterOverflowIsAtomic(t *testing.T) {
	body := goCollectorBody(1000, [][4]any{{"get", "404", 0, 0.0}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()
	client, _ := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	c := NewGoCollector("app", server.URL, client)

	assertGoCollect(t, c, false, 0, 0)
	body = goCollectorBody(1000, [][4]any{{"get", "404", maxExactCount, 1.0}})
	assertGoCollect(t, c, true, maxExactCount, 1.0)

	// Establish a fresh valid source segment, then advance it by one. Both
	// source populations are valid, but the collector-owned lifetime count
	// cannot represent their combined logical total exactly.
	body = goCollectorBody(1000, [][4]any{{"get", "404", 0, 0.0}})
	assertGoCollect(t, c, false, 0, 0)
	before := c.logical
	body = goCollectorBody(1000, [][4]any{{"get", "404", 1, 0.1}})
	result, err := c.Collect(context.Background())
	if !errors.Is(err, errGoMetricsContract) || hasSample(result, "http_requests_total") {
		t.Fatalf("overflow result=%#v err=%v", result, err)
	}
	if c.logical != before {
		t.Fatalf("logical counters partially advanced: before=%#v after=%#v", before, c.logical)
	}
}

func goCollectorBody(start int, tuples [][4]any) string {
	body := "# TYPE go_http_request_duration_seconds histogram\n"
	for _, tuple := range tuples {
		body += fmt.Sprintf("go_http_request_duration_seconds_count{method=%q,code=%q} %v\n", tuple[0], tuple[1], tuple[2])
		body += fmt.Sprintf("go_http_request_duration_seconds_sum{method=%q,code=%q} %v\n", tuple[0], tuple[1], tuple[3])
	}
	return body + fmt.Sprintf("# TYPE process_start_time_seconds gauge\nprocess_start_time_seconds %d\n", start)
}

func assertGoCollect(t *testing.T, c *GoCollector, wantHTTP bool, requests, duration float64) *CollectionResult {
	t.Helper()
	result, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if hasSample(result, "http_requests_total") != wantHTTP {
		t.Fatalf("HTTP availability=%v, want %v; samples=%#v", hasSample(result, "http_requests_total"), wantHTTP, result.Samples)
	}
	if wantHTTP {
		assertGoSampleValue(t, result, "http_requests_total", requests)
		assertGoSampleValue(t, result, "http_request_time_total_seconds", duration)
	}
	return result
}

func assertGoSampleValue(t *testing.T, result *CollectionResult, key string, want float64) {
	t.Helper()
	for _, sample := range result.Samples {
		if sample.Key == key {
			if !closeEnough(sample.Value, want) {
				t.Fatalf("%s=%v, want %v", key, sample.Value, want)
			}
			return
		}
	}
	t.Fatalf("sample %s missing from %#v", key, result.Samples)
}

func errorsIsGoContract(err error) bool {
	return errors.Is(err, errGoMetricsContract)
}
