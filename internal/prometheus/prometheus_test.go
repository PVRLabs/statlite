package prometheus

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testLimits() Limits {
	l := DefaultLimits
	l.MaxCompressedBytes = 1024
	l.MaxDecompressedBytes = 2048
	l.MaxSamples = 10
	l.MaxLabelsPerSample = 3
	l.MaxLabelBytes = 32
	l.MaxAggregationStates = 2
	return l
}

func TestParseTextAndOpenMetrics(t *testing.T) {
	tests := []struct {
		name    string
		format  Format
		input   string
		samples int
	}{
		{"text", TextFormat, "# HELP requests total\nrequests_total{method=\"get\\nline\"} +Inf 123\n", 1},
		{"openmetrics timestamps", OpenMetricsFormat, "request_seconds_bucket{le=\"1\"} 2 1520879607.123 # {trace_id=\"abc\"} 0.2 1520879607.789\n# EOF\n", 1},
		{"openmetrics optional final LF", OpenMetricsFormat, "metric 1\n# EOF", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []Sample
			stats, err := Parse(strings.NewReader(tt.input), tt.format, testLimits(), func(s Sample) error { got = append(got, s); return nil })
			if err != nil {
				t.Fatal(err)
			}
			if stats.Samples != tt.samples || len(got) != tt.samples {
				t.Fatalf("samples = %d/%d, want %d", stats.Samples, len(got), tt.samples)
			}
		})
	}
}

func TestParseRejectsMalformedAndHostileInput(t *testing.T) {
	tests := []struct {
		name, input string
		format      Format
		class       FailureClass
	}{
		{"bad metric", "9bad 1\n", TextFormat, FailureMalformed},
		{"unterminated labels", "good{label=\"x\" 1\n", TextFormat, FailureMalformed},
		{"bad escape", "good{label=\"\\q\"} 1\n", TextFormat, FailureMalformed},
		{"duplicate label", "good{label=\"a\",label=\"b\"} 1\n", TextFormat, FailureMalformed},
		{"bad timestamp", "good 1 yesterday\n", TextFormat, FailureMalformed},
		{"missing final LF", "good 1", TextFormat, FailureMalformed},
		{"text exemplar", "good 1 # {trace_id=\"abc\"} 1\n", TextFormat, FailureMalformed},
		{"fractional text timestamp", "good 1 1520879607.789\n", TextFormat, FailureMalformed},
		{"non-finite OpenMetrics timestamp", "good 1 NaN\n# EOF\n", OpenMetricsFormat, FailureMalformed},
		{"missing eof", "good 1\n", OpenMetricsFormat, FailureMalformed},
		{"data after eof", "good 1\n# EOF\nbad 2\n", OpenMetricsFormat, FailureMalformed},
		{"too many labels", "good{a=\"1\",b=\"2\",c=\"3\",d=\"4\"} 1\n", TextFormat, FailureUnsafe},
		{"large label", "good{label=\"abcdefghijklmnopqrstuvwxyz0123456789\"} 1\n", TextFormat, FailureUnsafe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(strings.NewReader(tt.input), tt.format, testLimits(), nil)
			assertClass(t, err, tt.class)
		})
	}
}

func TestParseSampleLimit(t *testing.T) {
	l := testLimits()
	l.MaxSamples = 2
	_, err := Parse(strings.NewReader("a 1\nb 2\nc 3\n"), TextFormat, l, nil)
	assertClass(t, err, FailureUnsafe)
}

func TestClientNegotiatesAndDecompresses(t *testing.T) {
	var accept string
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	_, _ = gz.Write([]byte("metric{kind=\"ok\"} 7\n# EOF\n"))
	_ = gz.Close()
	c, err := NewClient(time.Second, testLimits(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		accept = r.Header.Get("Accept")
		return testResponse(http.StatusOK, http.Header{"Content-Type": {"application/openmetrics-text; version=1.0.0; charset=utf-8; escaping=underscores"}, "Content-Encoding": {"gzip"}}, compressed.Bytes()), nil
	})
	stats, err := c.Scrape(context.Background(), "http://example.test/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Samples != 1 {
		t.Fatalf("samples = %d, want 1", stats.Samples)
	}
	if !strings.Contains(accept, "application/openmetrics-text") || !strings.Contains(accept, "text/plain") {
		t.Fatalf("Accept = %q", accept)
	}
	if strings.Count(accept, "escaping=underscores") != 2 {
		t.Fatalf("Accept = %q, want underscore escaping for both formats", accept)
	}
}

func TestClientClassifiesResponses(t *testing.T) {
	tests := []struct {
		name, contentType, encoding string
		status                      int
		class                       FailureClass
	}{
		{"authentication", "text/plain", "", http.StatusUnauthorized, FailureAuthentication},
		{"not found", "text/plain", "", http.StatusNotFound, FailureNotFound},
		{"http", "text/plain", "", http.StatusTooManyRequests, FailureHTTP},
		{"content type", "application/json", "", http.StatusOK, FailureContentType},
		{"encoding", "text/plain", "br", http.StatusOK, FailureContentType},
		{"unsupported text version", "text/plain; version=1.0.0", "", http.StatusOK, FailureContentType},
		{"unsupported OpenMetrics version", "application/openmetrics-text; version=99.0", "", http.StatusOK, FailureContentType},
		{"missing OpenMetrics version", "application/openmetrics-text", "", http.StatusOK, FailureContentType},
		{"unsupported escaping", "text/plain; version=0.0.4; escaping=allow-utf-8", "", http.StatusOK, FailureContentType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := NewClient(time.Second, testLimits(), nil)
			header := http.Header{"Content-Type": {tt.contentType}}
			if tt.encoding != "" {
				header.Set("Content-Encoding", tt.encoding)
			}
			c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return testResponse(tt.status, header, []byte("metric 1\n")), nil
			})
			_, err := c.Scrape(context.Background(), "http://example.test/metrics", nil)
			assertClass(t, err, tt.class)
		})
	}
}

func TestClientBoundsBytesAndRedirects(t *testing.T) {
	t.Run("compressed", func(t *testing.T) {
		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		_, _ = gz.Write([]byte(strings.Repeat("metric 1\n", 20)))
		_ = gz.Close()
		l := testLimits()
		l.MaxCompressedBytes = 10
		c, _ := NewClient(time.Second, l, nil)
		c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return testResponse(http.StatusOK, http.Header{"Content-Type": {"text/plain"}, "Content-Encoding": {"gzip"}}, compressed.Bytes()), nil
		})
		_, err := c.Scrape(context.Background(), "http://example.test/metrics", nil)
		assertClass(t, err, FailureUnsafe)
	})
	t.Run("decompressed", func(t *testing.T) {
		body := []byte(fmt.Sprintln(strings.Repeat("#", 3000)))
		l := testLimits()
		l.MaxCompressedBytes = 10
		c, _ := NewClient(time.Second, l, nil)
		c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return testResponse(http.StatusOK, http.Header{"Content-Type": {"text/plain"}}, body), nil
		})
		_, err := c.Scrape(context.Background(), "http://example.test/metrics", nil)
		assertClass(t, err, FailureUnsafe)
	})
	t.Run("identity ignores compressed limit", func(t *testing.T) {
		body := []byte("# " + strings.Repeat("x", 100) + "\nmetric 1\n")
		l := testLimits()
		l.MaxCompressedBytes = 10
		c, _ := NewClient(time.Second, l, nil)
		c.httpClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
			return testResponse(http.StatusOK, http.Header{"Content-Type": {"text/plain"}}, body), nil
		})
		stats, err := c.Scrape(context.Background(), "http://example.test/metrics", nil)
		if err != nil {
			t.Fatal(err)
		}
		if stats.Samples != 1 {
			t.Fatalf("samples = %d, want 1", stats.Samples)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		l := testLimits()
		l.MaxRedirects = 1
		c, _ := NewClient(time.Second, l, nil)
		c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return testResponse(http.StatusFound, http.Header{"Location": {r.URL.Path + "x"}}, nil), nil
		})
		_, err := c.Scrape(context.Background(), "http://example.test/metrics", nil)
		assertClass(t, err, FailureTransport)
	})
	t.Run("cross-origin redirect", func(t *testing.T) {
		requests := 0
		c, _ := NewClient(time.Second, testLimits(), &BasicAuth{Username: "user", Password: "pass"})
		c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			if requests > 1 {
				t.Fatalf("cross-origin redirect was followed to %s", r.URL)
			}
			return testResponse(http.StatusFound, http.Header{"Location": {"https://metrics.example.test/metrics"}}, nil), nil
		})
		_, err := c.Scrape(context.Background(), "https://example.test/metrics", nil)
		assertClass(t, err, FailureTransport)
		if requests != 1 {
			t.Fatalf("requests = %d, want 1", requests)
		}
	})
	t.Run("same-origin redirect", func(t *testing.T) {
		requests := 0
		c, _ := NewClient(time.Second, testLimits(), &BasicAuth{Username: "user", Password: "pass"})
		c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			requests++
			if requests == 1 {
				return testResponse(http.StatusFound, http.Header{"Location": {"https://EXAMPLE.test:443/next"}}, nil), nil
			}
			if _, _, ok := r.BasicAuth(); !ok {
				t.Fatal("Basic Auth missing after same-origin redirect")
			}
			return testResponse(http.StatusOK, http.Header{"Content-Type": {"text/plain; version=0.0.4; charset=utf-8"}}, []byte("metric 1\n")), nil
		})
		if _, err := c.Scrape(context.Background(), "https://example.test/metrics", nil); err != nil {
			t.Fatal(err)
		}
		if requests != 2 {
			t.Fatalf("requests = %d, want 2", requests)
		}
	})
}

func TestClientSendsBasicAuth(t *testing.T) {
	c, _ := NewClient(time.Second, testLimits(), &BasicAuth{Username: "user", Password: "pass"})
	c.httpClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "user" || password != "pass" {
			t.Fatalf("Basic Auth = %q/%q/%t", username, password, ok)
		}
		return testResponse(http.StatusOK, http.Header{"Content-Type": {"text/plain"}}, []byte("metric 1\n")), nil
	})
	if _, err := c.Scrape(context.Background(), "http://example.test/metrics", nil); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testResponse(status int, header http.Header, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body))}
}

func TestAccumulatorIsBounded(t *testing.T) {
	a, err := NewAccumulator(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add("one", 1); err != nil {
		t.Fatal(err)
	}
	if err := a.Add("one", 2); err != nil {
		t.Fatal(err)
	}
	if err := a.Add("two", 2); err != nil {
		t.Fatal(err)
	}
	assertClass(t, a.Add("three", 3), FailureUnsafe)
	if got, _ := a.Get("one"); got.Sum != 3 || got.Count != 2 {
		t.Fatalf("aggregate = %+v", got)
	}
}

func assertClass(t *testing.T, err error, want FailureClass) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want class %q", want)
	}
	var got *Error
	if !errors.As(err, &got) {
		t.Fatalf("error type = %T, want *Error: %v", err, err)
	}
	if got.Class != want {
		t.Fatalf("class = %q, want %q: %v", got.Class, want, err)
	}
}
