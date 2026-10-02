package inspect

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/prometheus"
)

func TestTypedMicronautInspectionUsesExactEndpointAndOneBoundedScrape(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/prometheus/" || r.URL.RawQuery != "scope=app" {
			t.Fatalf("request URL = %s, want exact Micronaut endpoint", r.URL)
		}
		if !strings.Contains(r.Header.Get("Accept"), "openmetrics-text") {
			t.Fatalf("Accept = %q, want Prometheus/OpenMetrics negotiation", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, `http_server_requests_seconds_count{method="GET",exception="none",status="200",uri="/"} 2
http_server_requests_seconds_sum{method="GET",exception="none",status="200",uri="/"} 0.5
process_cpu_usage 0.25
jvm_memory_used_bytes{area="heap",id="eden"} 1024
process_start_time_seconds 1770000000
process_uptime_seconds 12
`)
	}))
	defer server.Close()

	endpoint := server.URL + "/prometheus/?scope=app"
	result, err := Inspect(context.Background(), TargetMicronaut, endpoint)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one bounded scrape", requests)
	}
	if result.TargetType != TargetMicronaut || result.Endpoint != endpoint || result.Status != CompatibilityCompatible {
		t.Fatalf("result = %#v, want exact compatible Micronaut result", result)
	}
	want := []string{
		"http_requests_total", "http_404_total", "http_4xx_total", "http_5xx_total",
		"http_request_time_total_seconds", "process_cpu_usage", "jvm_heap_used_bytes",
		"process_start_time", "process_uptime",
	}
	if strings.Join(result.Capabilities, ",") != strings.Join(want, ",") {
		t.Fatalf("capabilities = %v, want %v", result.Capabilities, want)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", result.Warnings)
	}
}

func TestTypedMicronautInspectionDiscoversConventionalEndpointFromBaseURL(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, "process_cpu_usage 0.25\n")
	}))
	defer server.Close()

	result, err := Inspect(context.Background(), TargetMicronaut, server.URL)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if path != "/prometheus" || result.Endpoint != server.URL+"/prometheus" {
		t.Fatalf("path = %q, result = %#v, want discovered conventional endpoint", path, result)
	}
}

func TestTypedMicronautInspectionFallsBackFromContextRootToConventionalEndpoint(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if r.URL.Path == "/service/prometheus" {
			fmt.Fprint(w, "process_cpu_usage 0.25\n")
			return
		}
		fmt.Fprint(w, "unrelated_metric 1\n")
	}))
	defer server.Close()

	result, err := Inspect(context.Background(), TargetMicronaut, server.URL+"/service")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got := strings.Join(paths, ","); got != "/service,/service/prometheus" {
		t.Fatalf("paths = %q, want exact attempt followed by conventional endpoint", got)
	}
	if result.Endpoint != server.URL+"/service/prometheus" {
		t.Fatalf("endpoint = %q, want discovered context-root endpoint", result.Endpoint)
	}
}

func TestTypedMicronautInspectionDoesNotFallbackAfterHTTPFailure(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := Inspect(context.Background(), TargetMicronaut, server.URL+"/service")
	assertFailureKind(t, err, FailureIncomplete)
	if got := strings.Join(paths, ","); got != "/service" {
		t.Fatalf("paths = %q, want no conventional fallback after HTTP 503", got)
	}
}

func TestTypedMicronautInspectionReportsPartialCapabilitiesAndGeneratesNoProbeState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, "jvm_memory_used_bytes{area=\"heap\"} 1024\nprocess_uptime_seconds -1\n")
	}))
	defer server.Close()

	result, err := Inspect(context.Background(), TargetMicronaut, server.URL+"/prometheus")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if result.Status != CompatibilityPartial || strings.Join(result.Capabilities, ",") != "jvm_heap_used_bytes" {
		t.Fatalf("result = %#v, want partial heap-only result", result)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "process uptime") {
		t.Fatalf("warnings = %v, want focused partial warning", result.Warnings)
	}
}

func TestTypedMicronautInspectionRejectsUnrelatedMalformedOversizedAndAuthResponses(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   FailureKind
	}{
		{name: "unrelated", status: http.StatusOK, body: "unrelated_metric 1\n", want: FailureIncompatible},
		{name: "malformed", status: http.StatusOK, body: "process_cpu_usage nope\n", want: FailureIncomplete},
		{name: "auth", status: http.StatusUnauthorized, body: "unauthorized\n", want: FailureAuthRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain; version=0.0.4")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()

			_, err := Inspect(context.Background(), TargetMicronaut, server.URL+"/prometheus")
			assertFailureKind(t, err, tt.want)
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = io.WriteString(w, strings.Repeat("x", 1<<20+1))
	}))
	defer server.Close()
	_, err := Inspect(context.Background(), TargetMicronaut, server.URL+"/prometheus")
	assertFailureKind(t, err, FailureIncomplete)
}

func TestTypedMicronautInspectionRejectsUnsafeEndpointForms(t *testing.T) {
	for _, raw := range []string{
		"ftp://app.test/prometheus",
		"http://user:secret@app.test/prometheus",
		"http://app.test/prometheus#fragment",
		"http://app.test/prometheus#",
		"http://app.test/prometheus?scope=app#",
		"http://app.test:0/prometheus",
		"http://app.test:65536/prometheus",
		"http:prometheus",
		" http://app.test/prometheus",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Inspect(context.Background(), TargetMicronaut, raw); err == nil {
				t.Fatalf("Inspect(%q) error = nil", raw)
			}
		})
	}
}

func TestMicronautInspectionEndpointsPreservesCustomizedEndpointAndQuery(t *testing.T) {
	const endpoint = "http://app.test/custom/metrics?scope=app"
	got, err := micronautInspectionEndpoints(endpoint)
	if err != nil {
		t.Fatalf("micronautInspectionEndpoints() error = %v", err)
	}
	if len(got) != 1 || got[0] != endpoint {
		t.Fatalf("endpoints = %q, want only exact endpoint %q", got, endpoint)
	}
}

func TestTypedMicronautInspectionReportsUnreachableEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL + "/prometheus"
	server.Close()

	_, err := Inspect(context.Background(), TargetMicronaut, endpoint)
	assertFailureKind(t, err, FailureUnreachable)
}

func TestMicronautEndpointCandidates(t *testing.T) {
	for _, tt := range []struct{ suffix, want string }{
		{"", "/prometheus"}, {"/", "/prometheus"}, {"/?", "/?"},
		{"/svc%2Fwest/", "/svc%2Fwest/,/svc%2Fwest/prometheus"},
		{"/svc/%70rometheus", "/svc/%70rometheus,/svc/%70rometheus/prometheus"},
		{"/svc/prometheus/", "/svc/prometheus/"},
		{"/svc//", "/svc//,/svc//prometheus"},
		{"/svc/../custom", "/svc/../custom,/svc/../custom/prometheus"},
		{"/custom?x=1", "/custom?x=1"},
		{"/prometheus?scope=%23", "/prometheus?scope=%23"},
		{"/custom%23metrics?scope=app", "/custom%23metrics?scope=app"},
	} {
		t.Run(tt.suffix, func(t *testing.T) {
			endpoints, err := micronautInspectionEndpoints("http://app.test" + tt.suffix)
			if err != nil {
				t.Fatal(err)
			}
			for i := range endpoints {
				endpoints[i] = strings.TrimPrefix(endpoints[i], "http://app.test")
			}
			if strings.Join(endpoints, ",") != tt.want {
				t.Fatalf("endpoints = %v, want %s", endpoints, tt.want)
			}
		})
	}
}

func TestMicronautFallbackOnlyAfterConclusiveMiss(t *testing.T) {
	for _, tt := range []struct {
		name          string
		status        int
		content, body string
		requests      int
	}{
		{"404", 404, "text/plain", "missing", 2}, {"410", 410, "text/plain", "gone", 2},
		{"incompatible", 200, "text/plain", "unrelated 1\n", 2},
		{"malformed", 200, "text/plain", "process_cpu_usage nope\n", 1},
		{"html", 200, "text/html", "<html></html>", 1},
		{"forbidden", 403, "text/plain", "denied", 1},
		{"oversized", 200, "text/plain", strings.Repeat("# padding\n", 500000), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 2 {
					w.Header().Set("Content-Type", "text/plain")
					fmt.Fprint(w, "process_cpu_usage 0.2\n")
					return
				}
				w.Header().Set("Content-Type", tt.content)
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			result, err := Inspect(context.Background(), TargetMicronaut, server.URL+"/svc%2Fwest/")
			if requests != tt.requests {
				t.Fatalf("requests = %d", requests)
			}
			if tt.requests == 2 {
				if err != nil || result.Endpoint != server.URL+"/svc%2Fwest/prometheus" {
					t.Fatalf("result=%v err=%v", result, err)
				}
			} else if err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestMicronautInspectionCancelledDoesNotFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Inspect(ctx, TargetMicronaut, "http://example.test/context")
	if err == nil {
		t.Fatal("expected cancelled inspection")
	}
}

func TestUntypedInspectionDoesNotProbeMicronaut(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/prometheus" {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, `process_cpu_usage 0.25
http_server_requests_seconds_count{method="GET",status="200",uri="/",exception="none"} 1
`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	_, err := Application(context.Background(), server.URL)
	assertFailureKind(t, err, FailureUnrecognized)
	for _, p := range paths {
		if p == "/prometheus" {
			t.Fatal("unexpected Micronaut probe")
		}
	}
}

func TestMicronautInspectionDeadlineStopsFallback(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := Inspect(ctx, TargetMicronaut, server.URL+"/context")
	assertFailureKind(t, err, FailureIncomplete)
	if got := requests.Load(); got != 1 {
		t.Fatalf("requests=%d", got)
	}
}

func TestMicronautInspectionAgreesWithCollection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "process_cpu_usage 0.2\nprocess_uptime_seconds -1\n")
	}))
	defer server.Close()
	endpoint := server.URL + "/prometheus"
	inspection, err := Inspect(context.Background(), TargetMicronaut, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	client, err := prometheus.NewClientWithTransport(defaultTimeout, prometheus.DefaultLimits, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := collector.NewMicronautCollector("test", endpoint, client, nil).Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var keys, warnings []string
	for _, sample := range result.Samples {
		keys = append(keys, sample.Key)
	}
	for _, event := range result.Events {
		warnings = append(warnings, event.Message)
	}
	if !reflect.DeepEqual(keys, inspection.Capabilities) || !reflect.DeepEqual(warnings, inspection.Warnings) || inspection.Status != CompatibilityPartial {
		t.Fatalf("inspection=%v collection=%v", inspection, result)
	}
}
