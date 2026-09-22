package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMetricsContractAndCounters(t *testing.T) {
	startedAt := time.Date(2026, 9, 18, 12, 0, 0, 123, time.UTC)
	recorder := newStatLiteRecorder(startedAt, "READY")
	mux := http.NewServeMux()
	mux.HandleFunc(statLiteMetricsPath, recorder.metricsHandler)
	mux.HandleFunc("/ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/implicit", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/client-error", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	mux.HandleFunc("/failure", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	server := httptest.NewServer(recorder.middleware(mux))
	defer server.Close()

	for _, path := range []string{"/ok", "/implicit", "/missing", "/client-error", "/failure"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = response.Body.Close()
	}

	snapshot := getSnapshot(t, server.URL+statLiteMetricsPath+"?poll=1")
	if snapshot.Schema != "statlite-metrics/v1" || snapshot.Integration != "net-http" || snapshot.Status != "READY" {
		t.Fatalf("profile identity = %#v", snapshot)
	}
	if snapshot.StartedAt != startedAt.Format(time.RFC3339Nano) {
		t.Fatalf("started_at = %q, want %q", snapshot.StartedAt, startedAt.Format(time.RFC3339Nano))
	}
	if got, want := snapshot.Metrics.RequestsTotal, uint64(5); got != want {
		t.Fatalf("requests_total = %d, want %d", got, want)
	}
	if got, want := snapshot.Metrics.Responses404Total, uint64(1); got != want {
		t.Fatalf("responses_404_total = %d, want %d", got, want)
	}
	if got, want := snapshot.Metrics.Responses4xxTotal, uint64(2); got != want {
		t.Fatalf("responses_4xx_total = %d, want %d", got, want)
	}
	if got, want := snapshot.Metrics.Responses5xxTotal, uint64(1); got != want {
		t.Fatalf("responses_5xx_total = %d, want %d", got, want)
	}
	if snapshot.Metrics.RequestDurationSecondsTotal <= 0 {
		t.Fatalf("request_duration_seconds_total = %f, want positive", snapshot.Metrics.RequestDurationSecondsTotal)
	}
	if snapshot.Metrics.UptimeSeconds < 0 {
		t.Fatalf("uptime_seconds = %f, want non-negative", snapshot.Metrics.UptimeSeconds)
	}
	if snapshot.Metrics.ProcessCPUUsage < 0 {
		t.Fatalf("process_cpu_usage = %f, want non-negative", snapshot.Metrics.ProcessCPUUsage)
	}
	if snapshot.Metrics.RuntimeHeapUsedBytes == 0 {
		t.Fatal("runtime_heap_used_bytes = 0, want current Go heap allocation")
	}

	afterSecondPoll := getSnapshot(t, server.URL+statLiteMetricsPath)
	if afterSecondPoll.Metrics.RequestsTotal != snapshot.Metrics.RequestsTotal {
		t.Fatalf("metrics request changed requests_total from %d to %d", snapshot.Metrics.RequestsTotal, afterSecondPoll.Metrics.RequestsTotal)
	}
	if afterSecondPoll.StartedAt != snapshot.StartedAt {
		t.Fatalf("started_at changed from %q to %q", snapshot.StartedAt, afterSecondPoll.StartedAt)
	}
	if afterSecondPoll.Metrics.UptimeSeconds < snapshot.Metrics.UptimeSeconds {
		t.Fatalf("uptime_seconds decreased from %f to %f", snapshot.Metrics.UptimeSeconds, afterSecondPoll.Metrics.UptimeSeconds)
	}
	if afterSecondPoll.Metrics.RuntimeHeapUsedBytes == 0 {
		t.Fatal("runtime_heap_used_bytes disappeared on repeated collection")
	}
}

func TestConcurrentRequests(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	handler := recorder.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()

	const requests = 100
	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			response, err := http.Get(server.URL + "/missing")
			if err != nil {
				t.Errorf("GET /missing: %v", err)
				return
			}
			_ = response.Body.Close()
		}()
	}
	wg.Wait()

	snapshot := recorder.snapshot()
	if snapshot.Metrics.RequestsTotal != requests || snapshot.Metrics.Responses404Total != requests || snapshot.Metrics.Responses4xxTotal != requests {
		t.Fatalf("concurrent counters = %#v", snapshot.Metrics)
	}
}

func TestStartIdentityBelongsToApplicationFixture(t *testing.T) {
	start := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	firstRegistration := newStatLiteRecorder(start, "UP")
	secondRegistration := newStatLiteRecorder(start, "UP")
	if firstRegistration.snapshot().StartedAt != secondRegistration.snapshot().StartedAt {
		t.Fatal("re-registering with one application start changed started_at")
	}

	newApplicationStart := start.Add(time.Second)
	newApplication := newStatLiteRecorder(newApplicationStart, "UP")
	if firstRegistration.snapshot().StartedAt == newApplication.snapshot().StartedAt {
		t.Fatal("a new application fixture retained the previous started_at")
	}
}

func TestStartTimeKeepsMonotonicReadingForUptime(t *testing.T) {
	startedAt := time.Now()
	recorder := newStatLiteRecorder(startedAt, "UP")
	if recorder.startedAt != startedAt {
		t.Fatal("recorder changed the process start value used for uptime")
	}
	if recorder.serializedStartedAt != startedAt.UTC() {
		t.Fatal("serialized start value is not the UTC wall-clock copy")
	}
}

type headerRecordingResponseWriter struct {
	header   http.Header
	statuses []int
}

func (w *headerRecordingResponseWriter) Header() http.Header { return w.header }
func (w *headerRecordingResponseWriter) Write(p []byte) (int, error) {
	return len(p), nil
}
func (w *headerRecordingResponseWriter) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
}

func TestInformationalAndSwitchingStatusTransitions(t *testing.T) {
	t.Run("informational headers precede final status", func(t *testing.T) {
		underlying := &headerRecordingResponseWriter{header: make(http.Header)}
		_, status := wrapResponseWriter(underlying)
		status.WriteHeader(http.StatusEarlyHints)
		status.WriteHeader(http.StatusContinue)
		status.WriteHeader(http.StatusInternalServerError)
		status.WriteHeader(http.StatusNoContent)

		if got, want := fmt.Sprint(underlying.statuses), "[103 100 500]"; got != want {
			t.Fatalf("forwarded statuses = %s, want %s", got, want)
		}
		if got := status.code(); got != http.StatusInternalServerError {
			t.Fatalf("recorded status = %d, want 500", got)
		}
	})

	t.Run("switching protocols is terminal", func(t *testing.T) {
		underlying := &headerRecordingResponseWriter{header: make(http.Header)}
		_, status := wrapResponseWriter(underlying)
		status.WriteHeader(http.StatusSwitchingProtocols)
		status.WriteHeader(http.StatusInternalServerError)

		if got, want := fmt.Sprint(underlying.statuses), "[101]"; got != want {
			t.Fatalf("forwarded statuses = %s, want %s", got, want)
		}
		if got := status.code(); got != http.StatusSwitchingProtocols {
			t.Fatalf("recorded status = %d, want 101", got)
		}
	})
}

func TestPanicBoundary(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	panicking := recorder.middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("middleware unexpectedly recovered panic")
			}
		}()
		panicking.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
	}()
	if got := recorder.snapshot().Metrics.RequestsTotal; got != 0 {
		t.Fatalf("escaping panic recorded %d requests, want 0", got)
	}

	recoverInside := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			defer func() {
				if recover() != nil {
					http.Error(w, "recovered", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, req)
		})
	}
	recovered := recorder.middleware(recoverInside(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })))
	response := httptest.NewRecorder()
	recovered.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("recovered status = %d, want 500", response.Code)
	}
	snapshot := recorder.snapshot()
	if snapshot.Metrics.RequestsTotal != 1 || snapshot.Metrics.Responses5xxTotal != 1 {
		t.Fatalf("recovered panic counters = %#v", snapshot.Metrics)
	}
}

func TestRealServerFlushIsPreserved(t *testing.T) {
	release := make(chan struct{})
	recorder := newStatLiteRecorder(time.Now(), "UP")
	server := httptest.NewServer(recorder.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("real server writer lost http.Flusher")
			return
		}
		_, _ = io.WriteString(w, "first\n")
		flusher.Flush()
		<-release
		_, _ = io.WriteString(w, "second\n")
	})))
	defer server.Close()

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != "first\n" {
		t.Fatalf("first flushed line = %q, %v", line, err)
	}
	close(release)
	rest, err := io.ReadAll(reader)
	if err != nil || string(rest) != "second\n" {
		t.Fatalf("remaining body = %q, %v", rest, err)
	}
	_ = response.Body.Close()
}

func TestRealServerFlushCommitsImplicitOK(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	server := httptest.NewServer(recorder.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		w.WriteHeader(http.StatusInternalServerError)
	})))
	defer server.Close()

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("client status = %d, want 200", response.StatusCode)
	}
	snapshot := recorder.snapshot()
	if snapshot.Metrics.RequestsTotal != 1 || snapshot.Metrics.Responses5xxTotal != 0 {
		t.Fatalf("flush counters = %#v, want one request and no 5xx", snapshot.Metrics)
	}
}

func TestRealServerHijackPreservesCapabilityAndExcludesRequest(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	server := httptest.NewServer(recorder.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("real server writer lost http.Hijacker")
			return
		}
		connection, buffered, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer connection.Close()
		_, _ = buffered.WriteString("HTTP/1.1 500 Internal Server Error\r\nContent-Length: 8\r\n\r\nhijacked")
		_ = buffered.Flush()
	})))
	defer server.Close()

	address := strings.TrimPrefix(server.URL, "http://")
	connection, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, _ = fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", address)
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || string(body) != "hijacked" {
		t.Fatalf("hijacked body = %q, %v", body, err)
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("raw response status = %d, want 500", response.StatusCode)
	}

	snapshot := recorder.snapshot()
	if snapshot.Metrics.RequestsTotal != 0 || snapshot.Metrics.RequestDurationSecondsTotal != 0 || snapshot.Metrics.Responses5xxTotal != 0 {
		t.Fatalf("hijacked counters = %#v, want request excluded", snapshot.Metrics)
	}
}

type basicResponseWriter struct{ header http.Header }

func (w *basicResponseWriter) Header() http.Header       { return w.header }
func (*basicResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (*basicResponseWriter) WriteHeader(int)             {}

func TestUnsupportedCapabilitiesAreNotAdvertised(t *testing.T) {
	underlying := &basicResponseWriter{header: make(http.Header)}
	w, _ := wrapResponseWriter(underlying)
	if _, ok := w.(http.Flusher); ok {
		t.Fatal("wrapper advertised http.Flusher for unsupported writer")
	}
	if _, ok := w.(http.Hijacker); ok {
		t.Fatal("wrapper advertised http.Hijacker for unsupported writer")
	}
	if unwrapped := w.(interface{ Unwrap() http.ResponseWriter }).Unwrap(); unwrapped != underlying {
		t.Fatal("Unwrap did not return the underlying writer")
	}
}

func getSnapshot(t *testing.T, url string) statLiteSnapshot {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics status = %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("Content-Type = %q", contentType)
	}
	var snapshot statLiteSnapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
