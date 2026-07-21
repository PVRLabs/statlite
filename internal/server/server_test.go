package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
	"github.com/pvrlabs/statlite/internal/version"
)

func TestRootServesDashboardPage(t *testing.T) {
	statlite := New("127.0.0.1:0", nil)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("root request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("root status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read root body: %v", err)
	}
	content := string(body)
	for _, want := range []string{
		"cdn.jsdelivr.net/npm/chart.js",
		"/static/statlite-icon.png",
		`<span class="brand-stat">Stat</span><span class="brand-lite">Lite</span>`,
		"Current status",
		`aria-label="Target"`,
		`id="target-select"`,
		"Requests",
		"HTTP errors",
		"Average latency",
		"Recent events",
		`fetchJSON("/api/series" + query)`,
		`fetchJSON("/api/events" + query + "&limit=20")`,
		`case "unavailable":`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("root page missing %q", want)
		}
	}
}

func TestStatliteIconServedAsPNG(t *testing.T) {
	statlite := New("127.0.0.1:0", nil)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/static/statlite-icon.png")
	if err != nil {
		t.Fatalf("icon request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("icon status = %d, want 200", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("icon content-type = %q, want image/png", contentType)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read icon body: %v", err)
	}
	if len(body) < 8 || string(body[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatal("icon response is not a PNG")
	}
}

func TestHealthzIncludesSelfMetricsAndRequestCounters(t *testing.T) {
	statlite := New("127.0.0.1:0", nil)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/missing")
	if err != nil {
		t.Fatalf("missing request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404", resp.StatusCode)
	}

	resp, err = http.Get(server.URL + "/api/summary")
	if err != nil {
		t.Fatalf("summary request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("summary status = %d, want 500", resp.StatusCode)
	}

	resp, err = http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	if health.Status != "ok" || health.Timestamp == "" {
		t.Fatalf("health response = %#v, want status/timestamp", health)
	}
	if health.Version != version.Version {
		t.Fatalf("health version = %q, want %q", health.Version, version.Version)
	}
	if health.Statlite.UptimeSeconds <= 0 {
		t.Fatalf("uptime = %v, want positive", health.Statlite.UptimeSeconds)
	}
	if health.Statlite.ProcessStartTime.IsZero() {
		t.Fatal("process_start_time is zero, want server start time")
	}
	if health.Statlite.HTTP.RequestsTotal != 3 {
		t.Fatalf("requests_total = %d, want 3", health.Statlite.HTTP.RequestsTotal)
	}
	if health.Statlite.HTTP.NotFoundTotal != 1 {
		t.Fatalf("not_found_total = %d, want 1", health.Statlite.HTTP.NotFoundTotal)
	}
	if health.Statlite.HTTP.ServerErrorTotal != 1 {
		t.Fatalf("server_error_total = %d, want 1", health.Statlite.HTTP.ServerErrorTotal)
	}
	if health.Statlite.Runtime.MemorySysBytes == 0 || health.Statlite.Runtime.Goroutines == 0 {
		t.Fatalf("runtime = %#v, want runtime metrics", health.Statlite.Runtime)
	}
	if health.Statlite.Runtime.ProcessCPUUsage < 0 {
		t.Fatalf("process_cpu_usage = %v, want non-negative", health.Statlite.Runtime.ProcessCPUUsage)
	}
	if health.Statlite.Storage.Status != "unavailable" {
		t.Fatalf("storage status = %q, want unavailable without monitor", health.Statlite.Storage.Status)
	}
	if health.Statlite.Storage.LastStoredPollID != 0 {
		t.Fatalf("last_stored_poll_id = %d, want 0 without monitor", health.Statlite.Storage.LastStoredPollID)
	}
}

func TestHealthzStaysOKWhenTargetPollingFails(t *testing.T) {
	// Unreachable Actuator URL produces poll failures without breaking process health.
	client, err := collector.NewActuatorClient("http://127.0.0.1:1/actuator", 100*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon, err := monitor.New("app", collector.NewSpringActuatorCollector("app", client), store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/debug/poll-now")
	if err != nil {
		t.Fatalf("poll-now request: %v", err)
	}
	resp.Body.Close()

	resp, err = http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200 when only target polling fails", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	if health.Status != "ok" {
		t.Fatalf("process status = %q, want ok despite target poll failures", health.Status)
	}
	if health.Version != version.Version {
		t.Fatalf("health version = %q, want %q", health.Version, version.Version)
	}
	if health.Statlite.Storage.Status != "ok" {
		t.Fatalf("storage status = %q, want ok", health.Statlite.Storage.Status)
	}
	if health.Statlite.Polling.ConsecutiveFailures < 1 {
		t.Fatalf("consecutive_failures = %d, want at least 1", health.Statlite.Polling.ConsecutiveFailures)
	}
}

func TestHealthzReportsErrorWhenStorageUnhealthy(t *testing.T) {
	client, err := collector.NewActuatorClient("http://127.0.0.1:1/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	mon, err := monitor.New("app", collector.NewSpringActuatorCollector("app", client), store, time.Minute)
	if err != nil {
		store.Close()
		t.Fatalf("monitor.New() error = %v", err)
	}
	// Close the store so StorageHealthy fails while the process is still up.
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error = %v", err)
	}

	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("healthz status = %d, want 503 when storage is unhealthy", resp.StatusCode)
	}

	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	if health.Status != "error" {
		t.Fatalf("process status = %q, want error", health.Status)
	}
	if health.Version != version.Version {
		t.Fatalf("health version = %q, want %q", health.Version, version.Version)
	}
	if health.Statlite.Storage.Status != "error" {
		t.Fatalf("storage status = %q, want error", health.Statlite.Storage.Status)
	}
}

func TestDebugEndpointsAllowConcurrentPollAndLatestAccess(t *testing.T) {
	actuator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actuator/health":
			writeJSONForTest(t, w, map[string]string{"status": "UP"})
		case "/actuator/metrics/http.server.requests":
			writeJSONForTest(t, w, map[string]interface{}{
				"name": "http.server.requests",
				"measurements": []map[string]interface{}{
					{"statistic": "COUNT", "value": 1},
					{"statistic": "TOTAL_TIME", "value": 0.1},
				},
				"availableTags": []map[string]interface{}{
					{"tag": "status", "values": []string{"200"}},
				},
			})
		case "/actuator/metrics/jvm.memory.used", "/actuator/metrics/jvm.memory.max", "/actuator/metrics/process.cpu.usage", "/actuator/metrics/process.start.time":
			writeJSONForTest(t, w, map[string]interface{}{
				"name": r.URL.Path,
				"measurements": []map[string]interface{}{
					{"statistic": "VALUE", "value": 1},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer actuator.Close()

	client, err := collector.NewActuatorClient(actuator.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon, err := monitor.New("app", collector.NewSpringActuatorCollector("app", client), store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/debug/poll-now")
	if err != nil {
		t.Fatalf("initial poll-now request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initial poll-now status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("healthz request: %v", err)
	}
	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		resp.Body.Close()
		t.Fatalf("decode healthz: %v", err)
	}
	resp.Body.Close()
	if health.Statlite.Storage.Status != "ok" {
		t.Fatalf("storage status = %q, want ok", health.Statlite.Storage.Status)
	}
	if health.Statlite.Storage.LastStoredPollID == 0 {
		t.Fatalf("last_stored_poll_id = 0, want stored poll id")
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			resp, err := http.Get(server.URL + "/debug/poll-now")
			if err != nil {
				t.Errorf("poll-now request: %v", err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("poll-now status = %d, want 200", resp.StatusCode)
			}
		}()
		go func() {
			defer wg.Done()
			resp, err := http.Get(server.URL + "/debug/latest")
			if err != nil {
				t.Errorf("latest request: %v", err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("latest status = %d, want 200", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
}

func TestHandleSeriesReturnsDataAfterPoll(t *testing.T) {
	actuator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/actuator/health":
			writeJSONForTest(t, w, map[string]string{"status": "UP"})
		case "/actuator/metrics/http.server.requests":
			writeJSONForTest(t, w, map[string]interface{}{
				"name": "http.server.requests",
				"measurements": []map[string]interface{}{
					{"statistic": "COUNT", "value": 10},
					{"statistic": "TOTAL_TIME", "value": 1.0},
				},
				"availableTags": []map[string]interface{}{
					{"tag": "status", "values": []string{"200"}},
				},
			})
		default:
			writeJSONForTest(t, w, map[string]interface{}{
				"name": r.URL.Path,
				"measurements": []map[string]interface{}{
					{"statistic": "VALUE", "value": 1},
				},
			})
		}
	}))
	defer actuator.Close()

	client, err := collector.NewActuatorClient(actuator.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon, err := monitor.New("app", collector.NewSpringActuatorCollector("app", client), store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	pollResp, err := http.Get(server.URL + "/debug/poll-now")
	if err != nil {
		t.Fatalf("poll-now request: %v", err)
	}
	pollResp.Body.Close()
	if pollResp.StatusCode != http.StatusOK {
		t.Fatalf("poll-now status = %d, want 200", pollResp.StatusCode)
	}

	resp, err := http.Get(server.URL + "/api/series?range=1h")
	if err != nil {
		t.Fatalf("series request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("series status = %d, want 200", resp.StatusCode)
	}
	var seriesResp storage.Series
	if err := json.NewDecoder(resp.Body).Decode(&seriesResp); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(seriesResp.Points) != 1 {
		t.Fatalf("series points = %d, want 1", len(seriesResp.Points))
	}
	if seriesResp.Points[0].Requests != nil {
		t.Fatalf("first poll requests delta = %v, want nil (no previous)", *seriesResp.Points[0].Requests)
	}
}

func TestHandleSeriesReturnsDeltasAfterTwoPolls(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now()
	seq := &sequenceCollector{results: []collectResult{
		{result: &collector.CollectionResult{
			TargetName:     "app",
			PollStartedAt:  now.Add(-5 * time.Minute),
			PollFinishedAt: now.Add(-5*time.Minute + time.Second),
			HealthStatus:   "UP",
			Samples: []collector.MetricSample{
				{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: 10, Unit: "requests"},
				{Key: "http_404_total", Kind: collector.MetricKindCounter, Value: 1, Unit: "requests"},
				{Key: "http_4xx_total", Kind: collector.MetricKindCounter, Value: 2, Unit: "requests"},
				{Key: "http_5xx_total", Kind: collector.MetricKindCounter, Value: 0, Unit: "requests"},
				{Key: "http_request_time_total_seconds", Kind: collector.MetricKindCounter, Value: 1.0, Unit: "seconds"},
			},
		}},
		{result: &collector.CollectionResult{
			TargetName:     "app",
			PollStartedAt:  now.Add(-1 * time.Minute),
			PollFinishedAt: now.Add(-1*time.Minute + time.Second),
			HealthStatus:   "UP",
			Samples: []collector.MetricSample{
				{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: 35, Unit: "requests"},
				{Key: "http_404_total", Kind: collector.MetricKindCounter, Value: 3, Unit: "requests"},
				{Key: "http_4xx_total", Kind: collector.MetricKindCounter, Value: 6, Unit: "requests"},
				{Key: "http_5xx_total", Kind: collector.MetricKindCounter, Value: 2, Unit: "requests"},
				{Key: "http_request_time_total_seconds", Kind: collector.MetricKindCounter, Value: 3.5, Unit: "seconds"},
			},
		}},
	}}

	mon, err := monitor.New("app", seq, store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	for i := 0; i < 2; i++ {
		pollResp, err := http.Get(server.URL + "/debug/poll-now")
		if err != nil {
			t.Fatalf("poll-now %d request: %v", i, err)
		}
		pollResp.Body.Close()
		if pollResp.StatusCode != http.StatusOK {
			t.Fatalf("poll-now %d status = %d, want 200", i, pollResp.StatusCode)
		}
	}

	resp, err := http.Get(server.URL + "/api/series?range=1h")
	if err != nil {
		t.Fatalf("series request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("series status = %d, want 200", resp.StatusCode)
	}
	var seriesResp storage.Series
	if err := json.NewDecoder(resp.Body).Decode(&seriesResp); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(seriesResp.Points) != 2 {
		t.Fatalf("series points = %d, want 2", len(seriesResp.Points))
	}
	if seriesResp.Points[0].Requests != nil {
		t.Fatalf("first poll requests delta = %v, want nil (no previous)", *seriesResp.Points[0].Requests)
	}
	if seriesResp.Points[1].Requests == nil {
		t.Fatal("second poll requests delta = nil, want 25")
	}
	if *seriesResp.Points[1].Requests != 25 {
		t.Fatalf("second poll requests delta = %v, want 25", *seriesResp.Points[1].Requests)
	}
	if seriesResp.Points[1].HTTP404 == nil {
		t.Fatal("second poll 404 delta = nil, want 2")
	}
	if *seriesResp.Points[1].HTTP404 != 2 {
		t.Fatalf("second poll 404 delta = %v, want 2", *seriesResp.Points[1].HTTP404)
	}
	if seriesResp.Points[1].HTTP4xx == nil {
		t.Fatal("second poll 4xx delta = nil, want 4")
	}
	if *seriesResp.Points[1].HTTP4xx != 4 {
		t.Fatalf("second poll 4xx delta = %v, want 4", *seriesResp.Points[1].HTTP4xx)
	}
	if seriesResp.Points[1].HTTP5xx == nil {
		t.Fatal("second poll 5xx delta = nil, want 2")
	}
	if *seriesResp.Points[1].HTTP5xx != 2 {
		t.Fatalf("second poll 5xx delta = %v, want 2", *seriesResp.Points[1].HTTP5xx)
	}
	if seriesResp.Points[1].AverageLatencySeconds == nil {
		t.Fatal("second poll average latency = nil, want 0.1")
	}
	if *seriesResp.Points[1].AverageLatencySeconds != 0.1 {
		t.Fatalf("second poll average latency = %v, want 0.1", *seriesResp.Points[1].AverageLatencySeconds)
	}
}

func TestSummaryReturnsAllTargetsAndSelectedTarget(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	start := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	alpha := newServerTestMonitor(t, "alpha", store, &sequenceCollector{results: []collectResult{{
		result: &collector.CollectionResult{
			TargetName:     "alpha",
			PollStartedAt:  start,
			PollFinishedAt: start.Add(time.Second),
			HealthStatus:   "UP",
		},
	}}})
	beta := newServerTestMonitor(t, "beta", store, &sequenceCollector{results: []collectResult{{
		result: &collector.CollectionResult{
			TargetName:     "beta",
			PollStartedAt:  start,
			PollFinishedAt: start.Add(time.Second),
			HealthStatus:   "DOWN",
			Events:         []collector.CollectorEvent{{Severity: collector.EventSeverityError, Type: "collector_failed", Message: "boom"}},
		},
		err: fmt.Errorf("boom"),
	}}})
	manager := newServerTestManager(t, alpha, beta)
	if _, err := manager.PollNow(t.Context(), "alpha"); err != nil {
		t.Fatalf("PollNow(alpha) error = %v", err)
	}
	if _, err := manager.PollNow(t.Context(), "beta"); err == nil {
		t.Fatal("PollNow(beta) error = nil, want error")
	}

	statlite := NewWithManager("127.0.0.1:0", manager)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/summary?target=alpha")
	if err != nil {
		t.Fatalf("summary request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary status = %d, want 200", resp.StatusCode)
	}
	var summary SummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if len(summary.Targets) != 2 {
		t.Fatalf("summary targets = %d, want 2", len(summary.Targets))
	}
	if summary.Targets[0].Metadata.Name != "alpha" || summary.Targets[1].Metadata.Name != "beta" {
		t.Fatalf("summary target order = %#v, want alpha,beta", summary.Targets)
	}
	if summary.SelectedTarget.Name != "alpha" {
		t.Fatalf("selected target = %q, want alpha", summary.SelectedTarget.Name)
	}
	if summary.Latest == nil {
		t.Fatal("summary latest = nil, want selected target snapshot")
	}

	resp, err = http.Get(server.URL + "/api/summary?target=missing")
	if err != nil {
		t.Fatalf("summary default request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary default status = %d, want 200", resp.StatusCode)
	}
	summary = SummaryResponse{}
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode default summary: %v", err)
	}
	if summary.SelectedTarget.Name != "beta" {
		t.Fatalf("default selected target = %q, want beta with poll failure", summary.SelectedTarget.Name)
	}
}

func TestTargetAwareAPIsRouteToSelectedTarget(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	start := time.Now().UTC().Add(-10 * time.Minute)
	alpha := newServerTestMonitor(t, "alpha", store, &sequenceCollector{results: []collectResult{
		{result: serverTestResult("alpha", start, 10, 1, nil)},
		{result: serverTestResult("alpha", start.Add(time.Minute), 12, 1.2, nil)},
	}})
	beta := newServerTestMonitor(t, "beta", store, &sequenceCollector{results: []collectResult{
		{result: serverTestResult("beta", start, 100, 10, nil)},
		{result: serverTestResult("beta", start.Add(time.Minute), 150, 15, []collector.CollectorEvent{{Severity: collector.EventSeverityWarning, Type: "restart_detected", Message: "test restart"}})},
	}})
	manager := newServerTestManager(t, alpha, beta)
	for _, target := range []string{"alpha", "alpha", "beta", "beta"} {
		if _, err := manager.PollNow(t.Context(), target); err != nil {
			t.Fatalf("PollNow(%s) error = %v", target, err)
		}
	}

	statlite := NewWithManager("127.0.0.1:0", manager)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	latestResp, err := http.Get(server.URL + "/api/latest?target=beta")
	if err != nil {
		t.Fatalf("latest request: %v", err)
	}
	defer latestResp.Body.Close()
	if latestResp.StatusCode != http.StatusOK {
		t.Fatalf("latest status = %d, want 200", latestResp.StatusCode)
	}
	var latest storage.Snapshot
	if err := json.NewDecoder(latestResp.Body).Decode(&latest); err != nil {
		t.Fatalf("decode latest: %v", err)
	}
	if latest.Result.TargetName != "beta" {
		t.Fatalf("latest target = %q, want beta", latest.Result.TargetName)
	}

	seriesResp, err := http.Get(server.URL + "/api/series?target=beta&range=1h")
	if err != nil {
		t.Fatalf("series request: %v", err)
	}
	defer seriesResp.Body.Close()
	if seriesResp.StatusCode != http.StatusOK {
		t.Fatalf("series status = %d, want 200", seriesResp.StatusCode)
	}
	var series storage.Series
	if err := json.NewDecoder(seriesResp.Body).Decode(&series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(series.Points) != 2 {
		t.Fatalf("series points = %d, want 2", len(series.Points))
	}
	if series.Points[1].Requests == nil || *series.Points[1].Requests != 50 {
		t.Fatalf("beta requests delta = %v, want 50", series.Points[1].Requests)
	}

	eventsResp, err := http.Get(server.URL + "/api/events?target=beta&range=1h")
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer eventsResp.Body.Close()
	if eventsResp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", eventsResp.StatusCode)
	}
	var events []storage.Event
	if err := json.NewDecoder(eventsResp.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(events) != 1 || events[0].Type != "restart_detected" {
		t.Fatalf("events = %#v, want selected target restart event", events)
	}
}

func TestSeriesClampsCustomRangeToRetentionCutoff(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	oldStart := now.AddDate(0, 0, -2)
	retainedStart := now.Add(-time.Hour)
	appRunID, err := store.EnsureAppRun(t.Context(), "app", nil, oldStart)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}
	saveServerRetentionPoll(t, store, appRunID, oldStart, 10, nil)
	saveServerRetentionPoll(t, store, appRunID, retainedStart, 20, nil)

	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	cutoff := now.AddDate(0, 0, -1)
	statlite := NewWithManagerRetentionCutoff("127.0.0.1:0", mustSingleServerTestManager(t, mon), 1, func() time.Time {
		return cutoff
	})
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	start := url.QueryEscape(oldStart.Add(-time.Hour).Format(time.RFC3339))
	end := url.QueryEscape(now.Format(time.RFC3339))
	resp, err := http.Get(server.URL + "/api/series?start=" + start + "&end=" + end)
	if err != nil {
		t.Fatalf("series request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("series status = %d, want 200", resp.StatusCode)
	}
	var series storage.Series
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(series.Points) != 1 {
		t.Fatalf("series points = %d, want retained point only", len(series.Points))
	}
	if !series.Points[0].Timestamp.Equal(retainedStart) {
		t.Fatalf("point timestamp = %v, want %v", series.Points[0].Timestamp, retainedStart)
	}
	if series.Points[0].Requests != nil {
		t.Fatalf("first retained requests delta = %v, want nil without pre-cutoff baseline", *series.Points[0].Requests)
	}
}

func TestEventsClampsCustomRangeToRetentionCutoff(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	oldStart := now.AddDate(0, 0, -2)
	retainedStart := now.Add(-time.Hour)
	appRunID, err := store.EnsureAppRun(t.Context(), "app", nil, oldStart)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}
	saveServerRetentionPoll(t, store, appRunID, oldStart, 10, []collector.CollectorEvent{{Severity: collector.EventSeverityWarning, Type: "old_event", Message: "old"}})
	saveServerRetentionPoll(t, store, appRunID, retainedStart, 20, []collector.CollectorEvent{{Severity: collector.EventSeverityWarning, Type: "retained_event", Message: "retained"}})

	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	cutoff := now.AddDate(0, 0, -1)
	statlite := NewWithManagerRetentionCutoff("127.0.0.1:0", mustSingleServerTestManager(t, mon), 1, func() time.Time {
		return cutoff
	})
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	start := url.QueryEscape(oldStart.Add(-time.Hour).Format(time.RFC3339))
	end := url.QueryEscape(now.Format(time.RFC3339))
	resp, err := http.Get(server.URL + "/api/events?start=" + start + "&end=" + end)
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", resp.StatusCode)
	}
	var events []storage.Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events len = %d, want retained event only", len(events))
	}
	if events[0].Type != "retained_event" {
		t.Fatalf("event type = %q, want retained_event", events[0].Type)
	}
}

func TestHandleSeriesReturns400ForInvalidRange(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon, err := monitor.New("app", &noopCollector{}, store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/series?range=invalid")
	if err != nil {
		t.Fatalf("series request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("series status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleSeriesReturns500WithoutMonitor(t *testing.T) {
	statlite := New("127.0.0.1:0", nil)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/series?range=1h")
	if err != nil {
		t.Fatalf("series request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("series status = %d, want 500", resp.StatusCode)
	}
}

func TestHandleEventsReturnsEmptyWithoutData(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon, err := monitor.New("app", &noopCollector{}, store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/events?range=1h")
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", resp.StatusCode)
	}
	var events []storage.Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if events == nil {
		t.Fatal("events = nil, want non-nil empty slice")
	}
	if len(events) != 0 {
		t.Fatalf("events len = %d, want 0", len(events))
	}
}

func TestHandleEventsReturns400ForInvalidRange(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon, err := monitor.New("app", &noopCollector{}, store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/events?range=invalid")
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("events status = %d, want 400", resp.StatusCode)
	}
}

func TestHandleEventsReturns500WithoutMonitor(t *testing.T) {
	statlite := New("127.0.0.1:0", nil)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/events?range=1h")
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("events status = %d, want 500", resp.StatusCode)
	}
}

func TestHandleSummaryIgnoresInvalidRange(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/summary?range=bad")
	if err != nil {
		t.Fatalf("summary request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary status = %d, want 200", resp.StatusCode)
	}
	var summary SummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.LatestRestartStatus != restartStatusInvalidRange {
		t.Fatalf("latest_restart_status = %q, want %q", summary.LatestRestartStatus, restartStatusInvalidRange)
	}
}

func TestHandleSummarySurvivesRestartLookupFailure(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error = %v", err)
	}
	resp, err := http.Get(server.URL + "/api/summary?range=1h")
	if err != nil {
		t.Fatalf("summary request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary status = %d, want 200 despite restart lookup failure", resp.StatusCode)
	}
	var summary SummaryResponse
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.SelectedTarget.Name != "app" {
		t.Fatalf("selected target = %q, want app", summary.SelectedTarget.Name)
	}
	if summary.LatestRestartStatus != restartStatusUnavailable {
		t.Fatalf("latest_restart_status = %q, want %q", summary.LatestRestartStatus, restartStatusUnavailable)
	}
}

func TestDashboardBucketDurationPolicy(t *testing.T) {
	cases := []struct {
		r    DashboardRange
		want time.Duration
	}{
		{DashboardRange1H, 0},
		{DashboardRangeToday, 5 * time.Minute},
		{DashboardRange7D, 30 * time.Minute},
		{DashboardRange30D, 2 * time.Hour},
		{DashboardRangeCustom, 0},
	}
	for _, tc := range cases {
		if got := dashboardBucketDuration(tc.r); got != tc.want {
			t.Fatalf("dashboardBucketDuration(%q) = %v, want %v", tc.r, got, tc.want)
		}
	}
}

func TestHandleSeriesAggregatesDense7dWithScale(t *testing.T) {
	// Dense 7d history with 15-minute samples → 30-minute buckets, ~≤336 points.
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	// Align poll times to "now" so range=7d includes them.
	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-7 * 24 * time.Hour)
	appRunID, err := store.EnsureAppRun(t.Context(), "app", nil, start)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}
	// Every 15 minutes for 7d ≈ 673 raw points: two samples per bucket.
	step := 15 * time.Minute
	raw := 0
	for at := start; !at.After(end); at = at.Add(step) {
		saveServerRetentionPoll(t, store, appRunID, at, float64(raw+1), nil)
		raw++
	}

	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/series?range=7d")
	if err != nil {
		t.Fatalf("series request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("series status = %d, want 200", resp.StatusCode)
	}
	var series storage.Series
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(series.Points) == 0 {
		t.Fatal("series points empty")
	}
	// 7d / 30m = 336 buckets (natural scale ceiling, not hard truncation).
	const max7dBuckets = 336 + 2 // small slack for range edge alignment
	if len(series.Points) > max7dBuckets {
		t.Fatalf("series points = %d, want <= ~336 for 7d/30m scale", len(series.Points))
	}
	if len(series.Points) >= raw {
		t.Fatalf("series points = %d, want aggregated below raw %d", len(series.Points), raw)
	}
	for i, point := range series.Points {
		if point.Timestamp.Before(series.Start) {
			t.Fatalf("series point %d timestamp %v precedes range start %v", i, point.Timestamp, series.Start)
		}
	}
}

func TestHandleSeriesKeepsSparse1hResolution(t *testing.T) {
	// 12 points at 5-minute spacing with 1h range (1-minute buckets) → no shared buckets.
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	// Stay safely inside the live 1h window (not pinned to a stale "start" edge).
	base := time.Now().UTC().Add(-55 * time.Minute).Truncate(time.Minute)
	appRunID, err := store.EnsureAppRun(t.Context(), "app", nil, base)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}
	const n = 12
	for i := 0; i < n; i++ {
		saveServerRetentionPoll(t, store, appRunID, base.Add(time.Duration(i)*5*time.Minute), float64(i+1), nil)
	}

	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/series?range=1h")
	if err != nil {
		t.Fatalf("series request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("series status = %d, want 200", resp.StatusCode)
	}
	var series storage.Series
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(series.Points) != n {
		t.Fatalf("series points = %d, want full sparse resolution %d", len(series.Points), n)
	}
}

func TestHandleSeriesKeepsDense1hResolution(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	end := time.Now().UTC().Truncate(time.Minute)
	start := end.Add(-time.Hour)
	appRunID, err := store.EnsureAppRun(t.Context(), "app", nil, start)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}
	// Multiple samples per minute.
	raw := 0
	for at := start; !at.After(end); at = at.Add(15 * time.Second) {
		saveServerRetentionPoll(t, store, appRunID, at, float64(raw+1), nil)
		raw++
	}

	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/series?range=1h")
	if err != nil {
		t.Fatalf("series request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("series status = %d, want 200", resp.StatusCode)
	}
	var series storage.Series
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(series.Points) == 0 {
		t.Fatal("series points empty")
	}
	// Native 15-second resolution should remain, apart from samples that fall
	// just before the rolling request boundary.
	if len(series.Points) <= 62 {
		t.Fatalf("series points = %d, want native dense resolution above minute buckets", len(series.Points))
	}
	if len(series.Points) > raw {
		t.Fatalf("series points = %d, want no more than stored raw points %d", len(series.Points), raw)
	}
	for i, point := range series.Points {
		if point.PollID == 0 {
			t.Fatalf("series point %d omitted poll identity, indicating unexpected aggregation", i)
		}
	}
}

func TestHandleEventsHonorsCallerLimitAndDefaultsToAll(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	const eventLimit = 20

	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	appRunID, err := store.EnsureAppRun(t.Context(), "app", nil, base)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}
	const total = 30
	for i := 0; i < total; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		saveServerRetentionPoll(t, store, appRunID, at, float64(i+1), []collector.CollectorEvent{
			{Severity: collector.EventSeverityWarning, Type: "metric_fetch_failed", Message: fmt.Sprintf("event-%02d", i)},
		})
	}

	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	startQ := url.QueryEscape(base.Add(-time.Hour).Format(time.RFC3339))
	endQ := url.QueryEscape(base.Add(time.Hour).Format(time.RFC3339))
	requestURL := server.URL + "/api/events?start=" + startQ + "&end=" + endQ
	resp, err := http.Get(requestURL + "&limit=20")
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", resp.StatusCode)
	}
	var events []storage.Event
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(events) != eventLimit {
		t.Fatalf("events len = %d, want %d", len(events), eventLimit)
	}
	if events[0].Message != "event-29" {
		t.Fatalf("newest event = %q, want event-29", events[0].Message)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.After(events[i-1].Timestamp) {
			t.Fatalf("events not newest-first at %d", i)
		}
	}

	allResp, err := http.Get(requestURL)
	if err != nil {
		t.Fatalf("unlimited events request: %v", err)
	}
	defer allResp.Body.Close()
	if allResp.StatusCode != http.StatusOK {
		t.Fatalf("unlimited events status = %d, want 200", allResp.StatusCode)
	}
	var allEvents []storage.Event
	if err := json.NewDecoder(allResp.Body).Decode(&allEvents); err != nil {
		t.Fatalf("decode unlimited events: %v", err)
	}
	if len(allEvents) != total {
		t.Fatalf("unlimited events len = %d, want complete range of %d", len(allEvents), total)
	}
}

func TestHandleEventsRejectsInvalidLimit(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/events?range=1h&limit=bad")
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("events status = %d, want 400", resp.StatusCode)
	}
}

func TestSummaryLatestRestartIndependentOfEventsLimit(t *testing.T) {
	// Regression: early restart + >20 later warnings → events show only newest 20,
	// but summary.latest_restart still reports the earlier restart.
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	appRunID, err := store.EnsureAppRun(t.Context(), "app", nil, base)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}
	restartAt := base
	saveServerRetentionPoll(t, store, appRunID, restartAt, 1, []collector.CollectorEvent{
		{Severity: collector.EventSeverityWarning, Type: monitor.EventTypeRestartDetected, Message: "process start changed"},
	})
	for i := 1; i <= 25; i++ {
		saveServerRetentionPoll(t, store, appRunID, base.Add(time.Duration(i)*time.Minute), float64(i+1), []collector.CollectorEvent{
			{Severity: collector.EventSeverityWarning, Type: "metric_fetch_failed", Message: fmt.Sprintf("noise-%02d", i)},
		})
	}

	mon := newServerTestMonitor(t, "app", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	startQ := url.QueryEscape(base.Add(-time.Hour).Format(time.RFC3339))
	endQ := url.QueryEscape(base.Add(2 * time.Hour).Format(time.RFC3339))
	rangeQuery := "start=" + startQ + "&end=" + endQ
	const eventLimit = 20

	eventsResp, err := http.Get(server.URL + "/api/events?" + rangeQuery + "&limit=20")
	if err != nil {
		t.Fatalf("events request: %v", err)
	}
	defer eventsResp.Body.Close()
	if eventsResp.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", eventsResp.StatusCode)
	}
	var events []storage.Event
	if err := json.NewDecoder(eventsResp.Body).Decode(&events); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if len(events) != eventLimit {
		t.Fatalf("events len = %d, want %d", len(events), eventLimit)
	}
	for _, event := range events {
		if event.Type == monitor.EventTypeRestartDetected {
			t.Fatal("limited /api/events should not need to carry early restart_detected")
		}
	}

	summaryResp, err := http.Get(server.URL + "/api/summary?" + rangeQuery)
	if err != nil {
		t.Fatalf("summary request: %v", err)
	}
	defer summaryResp.Body.Close()
	if summaryResp.StatusCode != http.StatusOK {
		t.Fatalf("summary status = %d, want 200", summaryResp.StatusCode)
	}
	var summary SummaryResponse
	if err := json.NewDecoder(summaryResp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.LatestRestart == nil {
		t.Fatal("summary.latest_restart = nil, want earlier restart timestamp")
	}
	if !summary.LatestRestart.Equal(restartAt) {
		t.Fatalf("summary.latest_restart = %v, want %v", summary.LatestRestart, restartAt)
	}
	if summary.LatestRestartStatus != restartStatusFound {
		t.Fatalf("latest_restart_status = %q, want %q", summary.LatestRestartStatus, restartStatusFound)
	}
}

func TestHandleLatestReturns404WhenNoData(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon, err := monitor.New("app", &noopCollector{}, store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/latest")
	if err != nil {
		t.Fatalf("latest request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("latest status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleMonitorStatusReturnsJSON(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()
	mon, err := monitor.New("app", &noopCollector{}, store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/monitor/status")
	if err != nil {
		t.Fatalf("monitor status request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("monitor status = %d, want 200", resp.StatusCode)
	}
	var statusResp monitor.Status
	if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
		t.Fatalf("decode monitor status: %v", err)
	}
	if statusResp.ConsecutivePollFailures != 0 {
		t.Fatalf("ConsecutivePollFailures = %d, want 0", statusResp.ConsecutivePollFailures)
	}
}

func TestMultiTargetDebugEndpointsRouteToSelectedTarget(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	start := time.Now().UTC().Add(-10 * time.Minute)
	alpha := newServerTestMonitor(t, "alpha", store, &sequenceCollector{results: []collectResult{
		{result: serverTestResult("alpha", start, 10, 1, nil)},
		{result: serverTestResult("alpha", start.Add(time.Minute), 20, 2, nil)},
	}})
	beta := newServerTestMonitor(t, "beta", store, &sequenceCollector{results: []collectResult{
		{result: serverTestResult("beta", start, 100, 10, nil)},
	}})
	manager := newServerTestManager(t, alpha, beta)

	if _, err := manager.PollNow(t.Context(), "alpha"); err != nil {
		t.Fatalf("PollNow(alpha) error = %v", err)
	}

	statlite := NewWithManager("127.0.0.1:0", manager)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	t.Run("debug/latest returns data for polled target", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/debug/latest?target=alpha")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var snapshot storage.Snapshot
		if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if snapshot.Result.TargetName != "alpha" {
			t.Fatalf("target = %q, want alpha", snapshot.Result.TargetName)
		}
	})

	t.Run("debug/latest defaults to alpha when target=nonexistent", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/debug/latest?target=nonexistent")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var snapshot storage.Snapshot
		if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if snapshot.Result.TargetName != "alpha" {
			t.Fatalf("target = %q, want alpha (first target fallback)", snapshot.Result.TargetName)
		}
	})

	t.Run("debug/latest defaults to alpha when target omitted", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/debug/latest")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var snapshot storage.Snapshot
		if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if snapshot.Result.TargetName != "alpha" {
			t.Fatalf("target = %q, want alpha (first target fallback)", snapshot.Result.TargetName)
		}
	})

	t.Run("debug/latest returns 404 for unpolled target", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/debug/latest?target=beta")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("debug/poll-now defaults to alpha when target omitted", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/debug/poll-now")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var snapshot storage.Snapshot
		if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if snapshot.Result.TargetName != "alpha" {
			t.Fatalf("target = %q, want alpha (first target fallback)", snapshot.Result.TargetName)
		}
	})

	t.Run("debug/poll-now routes to beta", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/debug/poll-now?target=beta")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var snapshot storage.Snapshot
		if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if snapshot.Result.TargetName != "beta" {
			t.Fatalf("target = %q, want beta", snapshot.Result.TargetName)
		}
	})

	t.Run("debug/latest returns data for beta after poll", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/debug/latest?target=beta")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var snapshot storage.Snapshot
		if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if snapshot.Result.TargetName != "beta" {
			t.Fatalf("target = %q, want beta", snapshot.Result.TargetName)
		}
	})

	t.Run("monitor/status returns beta's status", func(t *testing.T) {
		resp, err := http.Get(server.URL + "/api/monitor/status?target=beta")
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}
		var statusResp monitor.Status
		if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if statusResp.LastSuccessfulPollAt == nil {
			t.Fatal("LastSuccessfulPollAt = nil, want set (beta was polled)")
		}
		if statusResp.ConsecutivePollFailures != 0 {
			t.Fatalf("ConsecutivePollFailures = %d, want 0", statusResp.ConsecutivePollFailures)
		}
	})
}

func TestHandleMonitorStatusReturns500WithoutMonitor(t *testing.T) {
	statlite := New("127.0.0.1:0", nil)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/api/monitor/status")
	if err != nil {
		t.Fatalf("monitor status request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("monitor status = %d, want 500", resp.StatusCode)
	}
}

func TestParseRangeDefaultsToOneHour(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/series", nil)
	start, end, dashRange, err := parseRange(req)
	if err != nil {
		t.Fatalf("parseRange() error = %v", err)
	}
	if end.Sub(start) != time.Hour {
		t.Fatalf("range = %v, want 1h", end.Sub(start))
	}
	if dashRange != DashboardRange1H {
		t.Fatalf("DashboardRange = %q, want %q", dashRange, DashboardRange1H)
	}
}

func TestParseRangeSupportsToday(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/series?range=today", nil)
	start, end, dashRange, err := parseRange(req)
	if err != nil {
		t.Fatalf("parseRange(today) error = %v", err)
	}
	now := time.Now().UTC()
	if start.Year() != now.Year() || start.Month() != now.Month() || start.Day() != now.Day() {
		t.Fatalf("today start = %v, want midnight today", start)
	}
	if start.Hour() != 0 || start.Minute() != 0 || start.Second() != 0 {
		t.Fatalf("today start = %v, want midnight", start)
	}
	if !end.After(start) {
		t.Fatalf("end %v is not after start %v", end, start)
	}
	if dashRange != DashboardRangeToday {
		t.Fatalf("DashboardRange = %q, want %q", dashRange, DashboardRangeToday)
	}
}

func TestParseRangeSupports7dAnd30d(t *testing.T) {
	for _, tc := range []struct {
		name string
		want DashboardRange
	}{
		{"7d", DashboardRange7D},
		{"30d", DashboardRange30D},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/series?range="+tc.name, nil)
		start, end, dashRange, err := parseRange(req)
		if err != nil {
			t.Fatalf("parseRange(%s) error = %v", tc.name, err)
		}
		if !start.Before(end) {
			t.Fatalf("%s: start %v not before end %v", tc.name, start, end)
		}
		if dashRange != tc.want {
			t.Fatalf("DashboardRange = %q, want %q", dashRange, tc.want)
		}
	}
}

func TestParseRangeErrorsOnInvalid(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/series?range=bad", nil)
	_, _, _, err := parseRange(req)
	if err == nil {
		t.Fatal("parseRange(bad) error = nil, want error")
	}
}

func TestParseRangeSupportsCustomStartEnd(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/series?start=2026-07-07T00:00:00Z&end=2026-07-07T12:00:00Z", nil)
	start, end, dashRange, err := parseRange(req)
	if err != nil {
		t.Fatalf("parseRange(custom) error = %v", err)
	}
	if !start.Equal(time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("start = %v, want 2026-07-07T00:00:00Z", start)
	}
	if !end.Equal(time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("end = %v, want 2026-07-07T12:00:00Z", end)
	}
	if dashRange != DashboardRangeCustom {
		t.Fatalf("DashboardRange = %q, want %q", dashRange, DashboardRangeCustom)
	}
}

func TestRetentionFallbackCutoffUsesCurrentTime(t *testing.T) {
	statlite := NewWithManagerRetention("127.0.0.1:0", nil, 90)

	first := statlite.retentionCutoff()
	time.Sleep(10 * time.Millisecond)
	second := statlite.retentionCutoff()

	if !second.After(first) {
		t.Fatalf("fallback retention cutoff did not advance: first=%v second=%v", first, second)
	}
}

func TestClearCutoffCounterBaselineClearsEarliestRetainedPoint(t *testing.T) {
	cutoff := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	oldRequests := 1.0
	firstRetainedRequests := 2.0
	laterRequests := 3.0
	series := &storage.Series{Points: []storage.SeriesPoint{
		{Timestamp: cutoff.Add(time.Hour), Requests: &laterRequests},
		{Timestamp: cutoff.Add(-time.Hour), Requests: &oldRequests},
		{Timestamp: cutoff.Add(time.Minute), Requests: &firstRetainedRequests},
	}}

	clearCutoffCounterBaseline(series, cutoff)

	if series.Points[0].Requests == nil || *series.Points[0].Requests != laterRequests {
		t.Fatalf("later retained requests = %v, want preserved", series.Points[0].Requests)
	}
	if series.Points[1].Requests == nil || *series.Points[1].Requests != oldRequests {
		t.Fatalf("pre-cutoff requests = %v, want preserved", series.Points[1].Requests)
	}
	if series.Points[2].Requests != nil {
		t.Fatalf("earliest retained requests = %v, want nil", *series.Points[2].Requests)
	}
}

func TestParseRangeCustomStartOnlyDefaultsEndToNow(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/series?start=2026-07-07T00:00:00Z", nil)
	start, end, dashRange, err := parseRange(req)
	if err != nil {
		t.Fatalf("parseRange(start only) error = %v", err)
	}
	if !start.Equal(time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("start = %v, want 2026-07-07T00:00:00Z", start)
	}
	now := time.Now().UTC()
	if d := now.Sub(end); d < 0 || d > time.Second {
		t.Fatalf("end = %v, want ~now (%v), diff=%v", end, now, d)
	}
	if dashRange != DashboardRangeCustom {
		t.Fatalf("DashboardRange = %q, want %q", dashRange, DashboardRangeCustom)
	}
}

func TestParseRangeErrorsOnStartAfterEnd(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/series?start=2026-07-07T12:00:00Z&end=2026-07-07T00:00:00Z", nil)
	_, _, _, err := parseRange(req)
	if err == nil {
		t.Fatal("parseRange(start>end) error = nil, want error")
	}
}

func TestParseQueryTimeParsesRFC3339(t *testing.T) {
	parsed, err := parseQueryTime("2026-07-07T10:00:00Z", "start")
	if err != nil {
		t.Fatalf("parseQueryTime() error = %v", err)
	}
	if !parsed.Equal(time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("parsed = %v, want 2026-07-07T10:00:00Z", parsed)
	}
}

func TestParseQueryTimeErrorsOnInvalidFormat(t *testing.T) {
	_, err := parseQueryTime("not-a-time", "start")
	if err == nil {
		t.Fatal("parseQueryTime(bad) error = nil, want error")
	}
}

func TestParseQueryTimeErrorsOnEmpty(t *testing.T) {
	_, err := parseQueryTime("", "start")
	if err == nil {
		t.Fatal("parseQueryTime(empty) error = nil, want error")
	}
}

type collectResult struct {
	result *collector.CollectionResult
	err    error
}

type sequenceCollector struct {
	results []collectResult
	index   int
}

func (c *sequenceCollector) Collect(context.Context) (*collector.CollectionResult, error) {
	if c.index >= len(c.results) {
		return nil, fmt.Errorf("sequenceCollector exhausted: called %d times with %d results", c.index+1, len(c.results))
	}
	result := c.results[c.index]
	c.index++
	return result.result, result.err
}

type noopCollector struct{}

func (c *noopCollector) Collect(context.Context) (*collector.CollectionResult, error) {
	return &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  time.Now(),
		PollFinishedAt: time.Now(),
	}, nil
}

func writeJSONForTest(t *testing.T, w http.ResponseWriter, value interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encoding response: %v", err)
	}
}

func newServerTestManager(t *testing.T, alpha, beta *monitor.Monitor) *monitor.Manager {
	t.Helper()
	manager, err := monitor.NewManager([]monitor.ManagedTarget{
		{
			Metadata: monitor.TargetMetadata{
				Name:           "alpha",
				Type:           "spring",
				Endpoint:       "http://alpha.example/actuator",
				EndpointSource: "actuator_base_url",
			},
			Monitor: alpha,
		},
		{
			Metadata: monitor.TargetMetadata{
				Name:           "beta",
				Type:           "statlite",
				Endpoint:       "http://beta.example/healthz",
				EndpointSource: "url",
			},
			Monitor: beta,
		},
	})
	if err != nil {
		t.Fatalf("monitor.NewManager() error = %v", err)
	}
	return manager
}

func mustSingleServerTestManager(t *testing.T, mon *monitor.Monitor) *monitor.Manager {
	t.Helper()
	manager, err := monitor.NewManager([]monitor.ManagedTarget{{
		Metadata: monitor.TargetMetadata{
			Name:           "app",
			Type:           "spring",
			Endpoint:       "http://app.example/actuator",
			EndpointSource: "actuator_base_url",
		},
		Monitor: mon,
	}})
	if err != nil {
		t.Fatalf("monitor.NewManager() error = %v", err)
	}
	return manager
}

func newServerTestMonitor(t *testing.T, name string, store *storage.Store, collector monitor.Collector) *monitor.Monitor {
	t.Helper()
	mon, err := monitor.New(name, collector, store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New(%q) error = %v", name, err)
	}
	return mon
}

func serverTestResult(target string, at time.Time, requests, requestSeconds float64, events []collector.CollectorEvent) *collector.CollectionResult {
	return &collector.CollectionResult{
		TargetName:     target,
		PollStartedAt:  at,
		PollFinishedAt: at.Add(time.Second),
		HealthStatus:   "UP",
		Samples: []collector.MetricSample{
			{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: requests, Unit: "requests"},
			{Key: "http_request_time_total_seconds", Kind: collector.MetricKindCounter, Value: requestSeconds, Unit: "seconds"},
		},
		Events: events,
	}
}

func saveServerRetentionPoll(t *testing.T, store *storage.Store, appRunID int64, startedAt time.Time, requests float64, events []collector.CollectorEvent) {
	t.Helper()
	result := &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  startedAt,
		PollFinishedAt: startedAt.Add(time.Second),
		HealthStatus:   "UP",
		Samples: []collector.MetricSample{
			{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: requests, Unit: "requests"},
		},
		Events: events,
	}
	if _, err := store.SaveCollectionResultWithAppRun(context.Background(), result, &appRunID); err != nil {
		t.Fatalf("SaveCollectionResultWithAppRun(%s) error = %v", startedAt, err)
	}
}
