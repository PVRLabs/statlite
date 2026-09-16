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

func TestTypedGoInspectionUsesExactEndpointAndReportsCapabilities(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/custom/metrics/" || r.URL.RawQuery != "scope=app" {
			t.Fatalf("request URL = %s, want exact Go endpoint", r.URL)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, `# TYPE go_http_request_duration_seconds histogram
go_http_request_duration_seconds_count{code="200",method="get"} 2
go_http_request_duration_seconds_sum{code="200",method="get"} 0.5
# TYPE go_memstats_heap_alloc_bytes gauge
go_memstats_heap_alloc_bytes 1024
`)
	}))
	defer server.Close()

	endpoint := server.URL + "/custom/metrics/?scope=app"
	result, err := Inspect(context.Background(), TargetGo, endpoint)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if requests != 1 || result.TargetType != TargetGo || result.Endpoint != endpoint || result.Status != CompatibilityCompatible {
		t.Fatalf("requests=%d result=%#v, want one exact compatible Go scrape", requests, result)
	}
	want := "runtime_heap_used_bytes,http_requests_total,http_404_total,http_4xx_total,http_5xx_total,http_request_time_total_seconds"
	if strings.Join(result.Capabilities, ",") != want || len(result.Warnings) != 0 {
		t.Fatalf("result = %#v, want capabilities %q without warnings", result, want)
	}
}

func TestTypedGoInspectionReportsRecognizedButUnobservedHTTPAsPartial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, "# TYPE go_http_request_duration_seconds histogram\n# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes 1024\n")
	}))
	defer server.Close()

	result, err := Inspect(context.Background(), TargetGo, server.URL+"/metrics")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if result.Status != CompatibilityPartial || strings.Join(result.Capabilities, ",") != "runtime_heap_used_bytes" || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "no observed requests") {
		t.Fatalf("result = %#v, want stable unobserved partial guidance", result)
	}
}

func TestTypedGoInspectionReportsRuntimeOnlyAsUnconfirmedPartial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, "# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes 1024\n")
	}))
	defer server.Close()

	result, err := Inspect(context.Background(), TargetGo, server.URL+"/metrics")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if result.Status != CompatibilityPartial || strings.Join(result.Capabilities, ",") != "runtime_heap_used_bytes" || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "generate HTTP traffic and inspect again") {
		t.Fatalf("result = %#v, want runtime-only unconfirmed guidance", result)
	}
}

func TestTypedGoInspectionKeepsValidHTTPWhenOptionalMetricsAreInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, `# TYPE go_http_request_duration_seconds histogram
go_http_request_duration_seconds_count{code="200",method="get"} 2
go_http_request_duration_seconds_sum{code="200",method="get"} 0.5
go_memstats_heap_alloc_bytes 1024
# TYPE process_start_time_seconds counter
process_start_time_seconds 1000
`)
	}))
	defer server.Close()

	result, err := Inspect(context.Background(), TargetGo, server.URL+"/metrics")
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if result.Status != CompatibilityPartial || len(result.Warnings) != 2 {
		t.Fatalf("result = %#v, want valid HTTP with two omitted optional concepts", result)
	}
	if !strings.Contains(result.Warnings[0], "heap") || !strings.Contains(result.Warnings[0], "omitted") || !strings.Contains(result.Warnings[1], "process start") || !strings.Contains(result.Warnings[1], "omitted") {
		t.Fatalf("warnings = %v, want focused optional omission guidance", result.Warnings)
	}
	if !strings.Contains(strings.Join(result.Capabilities, ","), "http_requests_total") || strings.Contains(strings.Join(result.Capabilities, ","), "runtime_heap_used_bytes") {
		t.Fatalf("capabilities = %v, want HTTP retained and invalid optional metrics omitted", result.Capabilities)
	}
}

func TestTypedGoInspectionRejectsUnrelatedInvalidAndTransportFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   FailureKind
	}{
		{name: "unrelated", status: http.StatusOK, body: "unrelated_metric 1\n", want: FailureIncompatible},
		{name: "invalid contract", status: http.StatusOK, body: "# TYPE go_http_request_duration_seconds histogram\ngo_http_request_duration_seconds_count{code=\"200\",method=\"GET\"} 1\ngo_http_request_duration_seconds_sum{code=\"200\",method=\"GET\"} 1\n", want: FailureIncompatible},
		{name: "malformed", status: http.StatusOK, body: "go_memstats_heap_alloc_bytes nope\n", want: FailureIncomplete},
		{name: "auth", status: http.StatusUnauthorized, body: "unauthorized\n", want: FailureAuthRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain; version=0.0.4")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			_, err := Inspect(context.Background(), TargetGo, server.URL+"/metrics")
			assertFailureKind(t, err, tt.want)
		})
	}
}

func TestTypedGoInspectionRejectsUnsafeEndpointForms(t *testing.T) {
	for _, raw := range []string{"", " http://app.test/metrics", "ftp://app.test/metrics", "http:///metrics", "http://user:secret@app.test/metrics", "http://app.test/metrics#fragment"} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Inspect(context.Background(), TargetGo, raw); err == nil {
				t.Fatalf("Inspect(%q) error = nil", raw)
			}
		})
	}
}

func TestUntypedInspectionDoesNotDiscoverGoExposition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, `# TYPE go_http_request_duration_seconds histogram
go_http_request_duration_seconds_count{code="200",method="get"} 2
go_http_request_duration_seconds_sum{code="200",method="get"} 0.5
`)
	}))
	defer server.Close()

	_, err := Application(context.Background(), server.URL)
	assertFailureKind(t, err, FailureUnrecognized)
}
