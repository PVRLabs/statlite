package inspect

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestInspectorRecognizesSpringUsingConventionalPaths(t *testing.T) {
	var paths []string
	i := testInspector(func(req *http.Request) (int, string) {
		paths = append(paths, req.URL.Path)
		switch req.URL.Path {
		case "/service/actuator/health":
			return http.StatusOK, `{"status":"UP"}`
		case "/service/statlite/metrics":
			return http.StatusNotFound, `{"error":"not found"}`
		case "/service/actuator/metrics":
			return http.StatusOK, `{"names":["jvm.memory.used"]}`
		default:
			t.Fatalf("unexpected path %q", req.URL.Path)
			return 0, ""
		}
	})
	base, err := parseApplicationURL("http://app.test/service/")
	if err != nil {
		t.Fatal(err)
	}

	result, err := i.application(context.Background(), base)
	if err != nil {
		t.Fatalf("application() error = %v", err)
	}
	if result.TargetType != TargetSpring || result.Endpoint != "http://app.test/service/actuator" {
		t.Fatalf("result = %#v", result)
	}
	if got := strings.Join(paths, ","); got != "/service/actuator/health,/service/statlite/metrics,/service/actuator/metrics" {
		t.Fatalf("paths = %q", got)
	}
}

func TestInspectorRecognizesPartialSpringHealth(t *testing.T) {
	i := testInspector(func(req *http.Request) (int, string) {
		switch req.URL.Path {
		case "/actuator/health":
			return http.StatusServiceUnavailable, `{"status":"DOWN"}`
		case "/statlite/metrics":
			return http.StatusNotFound, "missing"
		case "/actuator/metrics":
			return http.StatusForbidden, "private"
		default:
			t.Fatalf("unexpected path %q", req.URL.Path)
			return 0, ""
		}
	})
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	result, err := i.application(context.Background(), base)
	if err != nil {
		t.Fatalf("application() error = %v", err)
	}
	if result.TargetType != TargetSpring || len(result.Capabilities) != 1 || result.Capabilities[0] != "health" {
		t.Fatalf("result = %#v, want partial Spring result", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "metrics are unavailable" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestInspectorRecognizesFullAndMinimalStatliteMetrics(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "minimal", body: `{"schema":"statlite-metrics/v1","status":"UP"}`, want: 1},
		{name: "full", body: `{"schema":"statlite-metrics/v1","status":"UP","metrics":{}}`, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := testInspector(func(req *http.Request) (int, string) {
				if req.URL.Path == "/statlite/metrics" {
					return http.StatusOK, tt.body
				}
				return http.StatusNotFound, "missing"
			})
			base, err := parseApplicationURL("http://app.test")
			if err != nil {
				t.Fatal(err)
			}

			result, err := i.application(context.Background(), base)
			if err != nil {
				t.Fatalf("application() error = %v", err)
			}
			if result.TargetType != TargetStatliteMetrics || len(result.Capabilities) != tt.want {
				t.Fatalf("result = %#v, want %d capabilities", result, tt.want)
			}
		})
	}
}

func TestInspectorRejectsUnrelatedMalformedAndUnknownResponses(t *testing.T) {
	for _, body := range []string{
		`{"message":"not a supported payload"}`,
		`{"status":`,
		"<html>home</html>",
	} {
		t.Run(body, func(t *testing.T) {
			var paths []string
			i := testInspector(func(req *http.Request) (int, string) {
				paths = append(paths, req.URL.Path)
				return http.StatusOK, body
			})
			base, err := parseApplicationURL("http://app.test")
			if err != nil {
				t.Fatal(err)
			}

			_, err = i.application(context.Background(), base)
			assertFailureKind(t, err, FailureUnrecognized)
			if len(paths) != 3 {
				t.Fatalf("paths = %#v, want exactly three logical probes", paths)
			}
		})
	}
}

func TestUntypedInspectorDoesNotRecognizeQuarkusPayload(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/q/metrics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprint(w, `http_server_requests_seconds_count{method="GET",outcome="SUCCESS",status="200",uri="/"} 2
http_server_requests_seconds_sum{method="GET",outcome="SUCCESS",status="200",uri="/"} 0.5
process_start_time_seconds 1770000000
`)
	}))
	defer server.Close()

	endpoint := server.URL + "/q/metrics"
	_, err := Application(context.Background(), endpoint)
	assertFailureKind(t, err, FailureUnrecognized)
	if got := strings.Join(paths, ","); got != "/q/metrics/actuator/health,/q/metrics/statlite/metrics,/q/metrics" {
		t.Fatalf("untyped paths = %q, want three existing logical probes", got)
	}

	result, err := Inspect(context.Background(), TargetQuarkus, endpoint)
	if err != nil {
		t.Fatalf("typed Inspect() error = %v", err)
	}
	if result.TargetType != TargetQuarkus || result.Endpoint != endpoint {
		t.Fatalf("typed result = %#v, want Quarkus at exact endpoint", result)
	}
	if got := strings.Join(paths, ","); got != "/q/metrics/actuator/health,/q/metrics/statlite/metrics,/q/metrics,/q/metrics" {
		t.Fatalf("all paths = %q, want three untyped probes followed by one typed scrape", got)
	}
}

func TestUntypedInspectorDoesNotRecognizePrometheusExposition(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "generic Micrometer families", body: "jvm_memory_used_bytes{area=\"heap\"} 1024\nprocess_cpu_usage 0.25\n"},
		{name: "unrelated OpenMetrics", body: "# TYPE exporter_jobs gauge\nexporter_jobs 3\n# EOF\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var paths []string
			i := testInspector(func(req *http.Request) (int, string) {
				paths = append(paths, req.URL.Path)
				return http.StatusOK, tt.body
			})
			base, err := parseApplicationURL("http://app.test/metrics")
			if err != nil {
				t.Fatal(err)
			}

			_, err = i.application(context.Background(), base)
			assertFailureKind(t, err, FailureUnrecognized)
			if got := strings.Join(paths, ","); got != "/metrics/actuator/health,/metrics/statlite/metrics,/metrics" {
				t.Fatalf("paths = %q, want unchanged fixed probe order", got)
			}
		})
	}
}

func TestInspectorRejectsMultipleSupportedIntegrations(t *testing.T) {
	var paths []string
	i := testInspector(func(req *http.Request) (int, string) {
		paths = append(paths, req.URL.Path)
		switch req.URL.Path {
		case "/actuator/health":
			return http.StatusOK, `{"status":"UP"}`
		case "/statlite/metrics":
			return http.StatusOK, `{"schema":"statlite-metrics/v1","status":"UP"}`
		case "/q/metrics":
			t.Fatal("untyped inspection must not probe Quarkus metrics")
			return 0, ""
		default:
			t.Fatalf("unexpected path %q", req.URL.Path)
			return 0, ""
		}
	})
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	_, err = i.application(context.Background(), base)
	assertFailureKind(t, err, FailureMultiple)
	if got := strings.Join(paths, ","); got != "/actuator/health,/statlite/metrics" {
		t.Fatalf("paths = %q, want unchanged ambiguity handling with no Quarkus probe", got)
	}
}

func TestInspectorUsesApplicationURLOnlyAsStatliteFallback(t *testing.T) {
	var paths []string
	i := testInspector(func(req *http.Request) (int, string) {
		paths = append(paths, req.URL.Path)
		if req.URL.Path == "/service" {
			return http.StatusOK, `{"schema":"statlite-metrics/v1","status":"UP"}`
		}
		return http.StatusNotFound, "not found"
	})
	base, err := parseApplicationURL("http://app.test/service")
	if err != nil {
		t.Fatal(err)
	}

	result, err := i.application(context.Background(), base)
	if err != nil {
		t.Fatalf("application() error = %v", err)
	}
	if result.TargetType != TargetStatliteMetrics || result.Endpoint != base.String() {
		t.Fatalf("result = %#v", result)
	}
	if got := strings.Join(paths, ","); got != "/service/actuator/health,/service/statlite/metrics,/service" {
		t.Fatalf("paths = %q", got)
	}
}

func TestInspectorDoesNotFallbackAfterAuthenticationResponse(t *testing.T) {
	var paths []string
	i := testInspector(func(req *http.Request) (int, string) {
		paths = append(paths, req.URL.Path)
		if req.URL.Path == "/actuator/health" {
			return http.StatusUnauthorized, "unauthorized"
		}
		return http.StatusNotFound, "not found"
	})
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	_, err = i.application(context.Background(), base)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != FailureAuthRequired {
		t.Fatalf("application() error = %#v, want auth failure", err)
	}
	if got := strings.Join(paths, ","); got != "/actuator/health,/statlite/metrics" {
		t.Fatalf("paths = %q, want no application URL fallback", got)
	}
}

func TestInspectorDoesNotReturnSpringWhenOtherRecognitionIsIncomplete(t *testing.T) {
	i := &inspector{
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path == "/actuator/health" {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"status":"UP"}`)),
					Request:    req,
				}, nil
			}
			return nil, context.DeadlineExceeded
		})},
		timeout: time.Second,
	}
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	_, err = i.application(context.Background(), base)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != FailureIncomplete {
		t.Fatalf("application() error = %#v, want incomplete failure", err)
	}
}

func TestInspectorReportsConnectionFailureWithoutFallback(t *testing.T) {
	var calls int
	i := testInspectorError(func(*http.Request) error {
		calls++
		return errors.New("connection refused")
	})
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	_, err = i.application(context.Background(), base)
	assertFailureKind(t, err, FailureUnreachable)
	if calls != 2 {
		t.Fatalf("calls = %d, want two recognition probes and no fallback", calls)
	}
}

func TestInspectorEnforcesOverallTimeout(t *testing.T) {
	var calls int
	i := testInspectorError(func(req *http.Request) error {
		calls++
		<-req.Context().Done()
		return req.Context().Err()
	})
	i.timeout = 40 * time.Millisecond
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	_, err = i.application(context.Background(), base)
	assertFailureKind(t, err, FailureIncomplete)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("inspection took %s, want bounded timeout", elapsed)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want recognition probes only after incomplete health", calls)
	}
}

func TestInspectorReturnsSpringWhenMetricsCapabilityProbeTimesOut(t *testing.T) {
	i := &inspector{
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/actuator/health":
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"status":"UP"}`)),
					Request:    req,
				}, nil
			case "/statlite/metrics":
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("missing")),
					Request:    req,
				}, nil
			case "/actuator/metrics":
				<-req.Context().Done()
				return nil, req.Context().Err()
			default:
				t.Fatalf("unexpected path %q", req.URL.Path)
				return nil, nil
			}
		})},
		timeout: 40 * time.Millisecond,
	}
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	result, err := i.application(context.Background(), base)
	if err != nil {
		t.Fatalf("application() error = %v", err)
	}
	if result.TargetType != TargetSpring || len(result.Capabilities) != 1 || result.Capabilities[0] != "health" {
		t.Fatalf("result = %#v, want partial Spring result", result)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "metrics are unavailable" {
		t.Fatalf("warnings = %#v", result.Warnings)
	}
}

func TestInspectorRejectsUnknownStatliteSchema(t *testing.T) {
	var paths []string
	i := testInspector(func(req *http.Request) (int, string) {
		paths = append(paths, req.URL.Path)
		if req.URL.Path == "/actuator/health" {
			return http.StatusNotFound, "missing"
		}
		return http.StatusOK, `{"schema":"statlite-metrics/v2","status":"UP"}`
	})
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	_, err = i.application(context.Background(), base)
	assertFailureKind(t, err, FailureUnrecognized)
	if len(paths) != 3 {
		t.Fatalf("paths = %#v, want fallback after conclusive misses", paths)
	}
}

func TestInspectorRejectsOversizedRecognitionBody(t *testing.T) {
	var calls int
	large := strings.Repeat("x", maxResponseBytes+1)
	i := testInspector(func(req *http.Request) (int, string) {
		calls++
		return http.StatusOK, large
	})
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	_, err = i.application(context.Background(), base)
	assertFailureKind(t, err, FailureIncomplete)
	if calls != 2 {
		t.Fatalf("calls = %d, want no fallback after oversized recognition response", calls)
	}
}

func TestInspectorClosesEachBodyBeforeNextProbe(t *testing.T) {
	var previous *trackingBody
	var calls int
	i := &inspector{
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if previous != nil && !previous.closed {
				t.Fatalf("probe %d started before prior response body closed", calls+1)
			}
			calls++
			body := &trackingBody{Reader: strings.NewReader("not supported")}
			previous = body
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     make(http.Header),
				Body:       body,
				Request:    req,
			}, nil
		})},
		timeout: time.Second,
	}
	base, err := parseApplicationURL("http://app.test")
	if err != nil {
		t.Fatal(err)
	}

	_, err = i.application(context.Background(), base)
	assertFailureKind(t, err, FailureUnrecognized)
	if calls != 3 || previous == nil || !previous.closed {
		t.Fatalf("calls = %d, final body closed = %v", calls, previous != nil && previous.closed)
	}
}

func TestInspectorFollowsBoundedRedirectsAndKeepsConfiguredEndpoint(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/service/actuator/health":
			http.Redirect(w, r, "/moved-health", http.StatusFound)
		case "/moved-health":
			fmt.Fprint(w, `{"status":"UP"}`)
		case "/service/statlite/metrics":
			w.WriteHeader(http.StatusNotFound)
		case "/service/actuator/metrics":
			http.Redirect(w, r, "/moved-metrics", http.StatusTemporaryRedirect)
		case "/moved-metrics":
			fmt.Fprint(w, `{"names":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	result, err := Application(context.Background(), server.URL+"/service")
	if err != nil {
		t.Fatalf("Application() error = %v", err)
	}
	if result.Endpoint != server.URL+"/service/actuator" {
		t.Fatalf("endpoint = %q, want original configured endpoint", result.Endpoint)
	}
	if got := strings.Join(paths, ","); got != "/service/actuator/health,/moved-health,/service/statlite/metrics,/service/actuator/metrics,/moved-metrics" {
		t.Fatalf("wire paths = %q", got)
	}
}

func TestInspectorStopsAfterRedirectLimit(t *testing.T) {
	var healthRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/actuator/health" {
			healthRequests++
			http.Redirect(w, r, "/actuator/health?next="+fmt.Sprint(healthRequests), http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := Application(context.Background(), server.URL)
	assertFailureKind(t, err, FailureIncomplete)
	if healthRequests != maxRedirects+1 {
		t.Fatalf("health wire requests = %d, want %d", healthRequests, maxRedirects+1)
	}
}

func TestInspectDispatchesFrameworkTypesWithoutAddingSpringTypedBehavior(t *testing.T) {
	_, err := Inspect(context.Background(), TargetSpring, "http://app.test")
	assertFailureKind(t, err, FailureTypeUnavailable)
	if !strings.Contains(err.Error(), "not available") || !strings.Contains(err.Error(), "supported: quarkus") {
		t.Fatalf("Inspect(spring) error = %v, want unavailable type with current support", err)
	}
	_, err = Inspect(context.Background(), TargetType("prometheus"), "http://app.test")
	assertFailureKind(t, err, FailureTypeUnsupported)
	if !strings.Contains(err.Error(), "unsupported inspection type") || !strings.Contains(err.Error(), "supported: quarkus") || strings.Contains(err.Error(), "spring, quarkus") {
		t.Fatalf("Inspect(prometheus) error = %v, want current typed support only", err)
	}
}

func TestParseApplicationURLRejectsUnsafeForms(t *testing.T) {
	for _, raw := range []string{
		"ftp://app.test",
		"http:///missing-host",
		"http://user:secret@app.test",
		"http://app.test?debug=true",
		"http://app.test#fragment",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseApplicationURL(raw); err == nil {
				t.Fatalf("parseApplicationURL(%q) error = nil", raw)
			}
		})
	}
}

func testInspector(response func(*http.Request) (int, string)) *inspector {
	return &inspector{
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			status, body := response(req)
			return &http.Response{
				StatusCode: status,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		})},
		timeout: time.Second,
	}
}

func testInspectorError(response func(*http.Request) error) *inspector {
	return &inspector{
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, response(req)
		})},
		timeout: time.Second,
	}
}

func assertFailureKind(t *testing.T, err error, want FailureKind) {
	t.Helper()
	var failure *Failure
	if !errors.As(err, &failure) || failure.Kind != want {
		t.Fatalf("error = %#v, want failure kind %q", err, want)
	}
}

type trackingBody struct {
	io.Reader
	closed bool
}

func (b *trackingBody) Close() error {
	b.closed = true
	return nil
}
