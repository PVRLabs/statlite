package collector

import (
	"context"
	"net/http"
	"net/http/httptest"
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
