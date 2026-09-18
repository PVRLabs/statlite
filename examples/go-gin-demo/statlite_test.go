package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestMetricsContractAndCounters(t *testing.T) {
	startedAt := time.Date(2026, 9, 18, 12, 0, 0, 123, time.UTC)
	recorder := newStatLiteRecorder(startedAt, "READY")
	engine := newTestEngine(recorder, true)
	engine.GET("/ok", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	engine.GET("/implicit", func(*gin.Context) {})
	engine.GET("/client-error", func(c *gin.Context) { c.Status(http.StatusTeapot) })
	engine.GET("/failure", func(c *gin.Context) { c.Status(http.StatusBadGateway) })

	for _, path := range []string{"/ok", "/implicit", "/missing", "/client-error", "/failure"} {
		response := performRequest(engine, path)
		if path == "/missing" && response.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, response.Code)
		}
	}

	snapshot := getSnapshot(t, engine, statLiteMetricsPath+"?poll=1")
	if snapshot.Schema != "statlite-metrics/v1" || snapshot.Integration != "gin" || snapshot.Status != "READY" {
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
	if snapshot.Metrics.RequestDurationSecondsTotal <= 0 || snapshot.Metrics.UptimeSeconds < 0 {
		t.Fatalf("timing metrics = %#v", snapshot.Metrics)
	}

	afterSecondPoll := getSnapshot(t, engine, statLiteMetricsPath)
	if afterSecondPoll.Metrics.RequestsTotal != snapshot.Metrics.RequestsTotal {
		t.Fatalf("metrics request changed requests_total from %d to %d", snapshot.Metrics.RequestsTotal, afterSecondPoll.Metrics.RequestsTotal)
	}
}

func TestRecoveryOrderingRecordsPreCommitPanic(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	engine := newTestEngine(recorder, true)
	engine.GET("/panic", func(*gin.Context) { panic("boom") })

	response := performRequest(engine, "/panic")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want 500", response.Code)
	}
	snapshot := recorder.snapshot()
	if snapshot.Metrics.RequestsTotal != 1 || snapshot.Metrics.Responses5xxTotal != 1 {
		t.Fatalf("panic counters = %#v", snapshot.Metrics)
	}
}

func TestWrongRecoveryOrderingDoesNotRecordPreCommitPanic(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	engine := gin.New()
	engine.Use(gin.Recovery())
	engine.Use(recorder.middleware)
	engine.GET("/panic", func(*gin.Context) { panic("boom") })

	response := performRequest(engine, "/panic")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("panic status = %d, want 500", response.Code)
	}
	snapshot := recorder.snapshot()
	if snapshot.Metrics.RequestsTotal == 1 && snapshot.Metrics.Responses5xxTotal == 1 {
		t.Fatalf("wrong ordering unexpectedly recorded the expected result: %#v", snapshot.Metrics)
	}
}

func TestPostWritePanicRetainsCommittedStatus(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	engine := newTestEngine(recorder, true)
	engine.GET("/panic-after-write", func(c *gin.Context) {
		c.String(http.StatusAccepted, "accepted")
		panic("boom")
	})

	response := performRequest(engine, "/panic-after-write")
	if response.Code != http.StatusAccepted {
		t.Fatalf("post-write panic status = %d, want 202", response.Code)
	}
	snapshot := recorder.snapshot()
	if snapshot.Metrics.RequestsTotal != 1 || snapshot.Metrics.Responses5xxTotal != 0 {
		t.Fatalf("post-write panic counters = %#v", snapshot.Metrics)
	}
}

func TestConcurrentRequests(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	engine := newTestEngine(recorder, true)
	engine.GET("/missing-by-design", func(c *gin.Context) { c.Status(http.StatusNotFound) })

	const requests = 100
	var wg sync.WaitGroup
	wg.Add(requests)
	for range requests {
		go func() {
			defer wg.Done()
			response := performRequest(engine, "/missing-by-design")
			if response.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", response.Code)
			}
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

	newApplication := newStatLiteRecorder(start.Add(time.Second), "UP")
	if firstRegistration.snapshot().StartedAt == newApplication.snapshot().StartedAt {
		t.Fatal("a new application fixture retained the previous started_at")
	}
}

func TestStreamingUsesGinWriter(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	engine := newTestEngine(recorder, true)
	engine.GET("/stream", func(c *gin.Context) {
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write([]byte("first\n"))
		c.Writer.Flush()
		_, _ = c.Writer.Write([]byte("second\n"))
	})

	response := performRequest(engine, "/stream")
	if response.Code != http.StatusOK || response.Body.String() != "first\nsecond\n" || !response.Flushed {
		t.Fatalf("stream response = status %d, body %q, flushed %v", response.Code, response.Body.String(), response.Flushed)
	}
	if snapshot := recorder.snapshot(); snapshot.Metrics.RequestsTotal != 1 {
		t.Fatalf("stream counters = %#v", snapshot.Metrics)
	}
}

func TestProtocolUpgradeIsExcludedAfterRealServerHijack(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	engine := newTestEngine(recorder, true)
	engine.GET("/upgrade", func(c *gin.Context) {
		connection, buffered, err := c.Writer.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		defer connection.Close()
		_, _ = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: example\r\nConnection: Upgrade\r\n\r\n")
		_ = buffered.Flush()
	})
	server := httptest.NewServer(engine)
	defer server.Close()

	address := strings.TrimPrefix(server.URL, "http://")
	connection, err := net.DialTimeout("tcp", address, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, _ = fmt.Fprintf(connection, "GET /upgrade HTTP/1.1\r\nHost: %s\r\nConnection: keep-alive, Upgrade\r\nUpgrade: example\r\n\r\n", address)
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("raw response status = %d, want 101", response.StatusCode)
	}

	snapshot := recorder.snapshot()
	if snapshot.Metrics.RequestsTotal != 0 || snapshot.Metrics.RequestDurationSecondsTotal != 0 {
		t.Fatalf("protocol upgrade counters = %#v, want excluded", snapshot.Metrics)
	}
}

func TestRejectedAdvertisedUpgradeIsExcluded(t *testing.T) {
	recorder := newStatLiteRecorder(time.Now(), "UP")
	engine := newTestEngine(recorder, true)
	engine.GET("/upgrade", func(c *gin.Context) { c.Status(http.StatusBadRequest) })

	request := httptest.NewRequest(http.MethodGet, "/upgrade", nil)
	request.Header.Set("Connection", "keep-alive, Upgrade")
	request.Header.Set("Upgrade", "example")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("rejected upgrade status = %d, want 400", response.Code)
	}

	snapshot := recorder.snapshot()
	if snapshot.Metrics.RequestsTotal != 0 || snapshot.Metrics.Responses4xxTotal != 0 {
		t.Fatalf("rejected upgrade counters = %#v, want excluded", snapshot.Metrics)
	}
}

func newTestEngine(recorder *statLiteRecorder, recoveryAfterMetrics bool) *gin.Engine {
	engine := gin.New()
	if recoveryAfterMetrics {
		engine.Use(recorder.middleware)
		engine.Use(gin.Recovery())
	}
	engine.GET(statLiteMetricsPath, recorder.metricsHandler)
	return engine
}

func performRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	return response
}

func getSnapshot(t *testing.T, handler http.Handler, path string) statLiteSnapshot {
	t.Helper()
	response := performRequest(handler, path)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200", path, response.Code)
	}
	var snapshot statLiteSnapshot
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	return snapshot
}
