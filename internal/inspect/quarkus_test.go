package inspect

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTypedQuarkusInspectionUsesExactEndpointAndOneBoundedScrape(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/q/metrics/" || r.URL.RawQuery != "scope=app" {
			t.Fatalf("request URL = %s, want exact Quarkus endpoint", r.URL)
		}
		if !strings.Contains(r.Header.Get("Accept"), "openmetrics-text") {
			t.Fatalf("Accept = %q, want Prometheus/OpenMetrics negotiation", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200",uri="/"} 2
http_server_requests_seconds_sum{method="GET",outcome="SUCCESS",status="200",uri="/"} 0.5
process_cpu_usage 0.25
jvm_memory_used_bytes{area="heap",id="eden"} 1024
process_start_time_seconds 1770000000
process_uptime_seconds 12
`)
	}))
	defer server.Close()

	endpoint := server.URL + "/q/metrics/?scope=app"
	result, err := Inspect(context.Background(), TargetQuarkus, endpoint)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want one bounded scrape", requests)
	}
	if result.TargetType != TargetQuarkus || result.Endpoint != endpoint || result.Status != CompatibilityCompatible {
		t.Fatalf("result = %#v, want exact compatible Quarkus result", result)
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

func TestTypedQuarkusInspectionDiscoversConventionalEndpointFromBaseURL(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, "process_cpu_usage 0.25\n")
	}))
	defer server.Close()

	result, err := Inspect(context.Background(), TargetQuarkus, server.URL)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if path != "/q/metrics" || result.Endpoint != server.URL+"/q/metrics" {
		t.Fatalf("path = %q, result = %#v, want discovered conventional endpoint", path, result)
	}
}

func TestTypedQuarkusInspectionFallsBackFromContextRootToConventionalEndpoint(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		if r.URL.Path == "/service/q/metrics" {
			fmt.Fprint(w, "process_cpu_usage 0.25\n")
			return
		}
		fmt.Fprint(w, "not metrics\n")
	}))
	defer server.Close()

	result, err := Inspect(context.Background(), TargetQuarkus, server.URL+"/service")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if got := strings.Join(paths, ","); got != "/service,/service/q/metrics" {
		t.Fatalf("paths = %q, want exact attempt followed by conventional endpoint", got)
	}
	if result.Endpoint != server.URL+"/service/q/metrics" {
		t.Fatalf("endpoint = %q, want discovered context-root endpoint", result.Endpoint)
	}
}

func TestTypedQuarkusInspectionDoesNotFallbackAfterHTTPFailure(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	_, err := Inspect(context.Background(), TargetQuarkus, server.URL+"/service")
	assertFailureKind(t, err, FailureIncomplete)
	if got := strings.Join(paths, ","); got != "/service" {
		t.Fatalf("paths = %q, want no conventional fallback after HTTP 503", got)
	}
}

func TestTypedQuarkusInspectionReportsPartialCapabilitiesAndGeneratesNoProbeState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, "jvm_memory_used_bytes{area=\"heap\"} 1024\nprocess_uptime_seconds -1\n")
	}))
	defer server.Close()

	result, err := Inspect(context.Background(), TargetQuarkus, server.URL+"/q/metrics")
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

func TestTypedQuarkusInspectionRejectsUnrelatedMalformedOversizedAndAuthResponses(t *testing.T) {
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

			_, err := Inspect(context.Background(), TargetQuarkus, server.URL+"/q/metrics")
			assertFailureKind(t, err, tt.want)
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = io.WriteString(w, strings.Repeat("x", 1<<20+1))
	}))
	defer server.Close()
	_, err := Inspect(context.Background(), TargetQuarkus, server.URL+"/q/metrics")
	assertFailureKind(t, err, FailureIncomplete)
}

func TestTypedQuarkusInspectionRejectsUnsafeEndpointForms(t *testing.T) {
	for _, raw := range []string{
		"ftp://app.test/q/metrics",
		"http://user:secret@app.test/q/metrics",
		"http://app.test/q/metrics#fragment",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Inspect(context.Background(), TargetQuarkus, raw); err == nil {
				t.Fatalf("Inspect(%q) error = nil", raw)
			}
		})
	}
}

func TestParseQuarkusEndpointPreservesCustomizedEndpointAndQuery(t *testing.T) {
	const endpoint = "http://app.test/custom/metrics?scope=app"
	got, err := parseQuarkusEndpoint(endpoint)
	if err != nil {
		t.Fatalf("parseQuarkusEndpoint() error = %v", err)
	}
	if got != endpoint {
		t.Fatalf("endpoint = %q, want exact %q", got, endpoint)
	}
}

func TestTypedQuarkusInspectionReportsUnreachableEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL + "/q/metrics"
	server.Close()

	_, err := Inspect(context.Background(), TargetQuarkus, endpoint)
	assertFailureKind(t, err, FailureUnreachable)
}
