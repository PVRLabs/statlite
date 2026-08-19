package inspect

import (
	"context"
	"errors"
	"io"
	"net/http"
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
