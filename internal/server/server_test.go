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
	"sync/atomic"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/dashboard"
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
		`<script src="` + dashboard.ChartJSPath + `"></script>`,
		`url("` + dashboard.OrbitronFontPath + `")`,
		"/static/statlite-icon.png",
		`<script src="` + dashboard.ScriptPath() + `" defer></script>`,
		`<span class="brand-stat">Stat</span><span class="brand-lite">Lite</span>`,
		`<span class="brand-version">` + version.Version + `</span>`,
		"Current status",
		`aria-label="Target"`,
		`id="target-select"`,
		"Requests",
		"HTTP errors",
		"Average latency",
		`id="application-section"`,
		"Application",
		`id="process-section"`,
		"Process",
		`id="host-section"`,
		"Host resources",
		"Host runtime",
		`id="host-runtime-chart"`,
		`id="host-disk-chart"`,
		"Recent events",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("root page missing %q", want)
		}
	}
	if strings.Contains(content, "{{dashboard_version}}") {
		t.Fatal("root page retains dashboard version placeholder")
	}
	if strings.Contains(content, `<div class="brand" aria-label="StatLite">`) {
		t.Fatal("root page keeps a redundant brand aria-label")
	}
	for _, legacy := range []string{"statlite-health", `case "statlite":`, "host_cpu_usage"} {
		if strings.Contains(content, legacy) {
			t.Fatalf("root page retains legacy collector help %q", legacy)
		}
	}
	if latency, process, host := strings.Index(content, "Average latency"), strings.Index(content, `id="process-section"`), strings.Index(content, `id="host-section"`); latency == -1 || process == -1 || host == -1 || !(latency < process && process < host) {
		t.Fatalf("dashboard cards are not ordered as application, process, then host: latency=%d process=%d host=%d", latency, process, host)
	}
}

func TestDashboardScriptServedAsJavaScript(t *testing.T) {
	statlite := New("127.0.0.1:0", nil)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	resp, err := http.Get(server.URL + dashboard.ScriptPath())
	if err != nil {
		t.Fatalf("dashboard script request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard script status = %d, want 200", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); contentType != "application/javascript; charset=utf-8" {
		t.Fatalf("dashboard script content-type = %q", contentType)
	}
	if cacheControl := resp.Header.Get("Cache-Control"); cacheControl != "public, max-age=31536000, immutable" {
		t.Fatalf("dashboard script Cache-Control = %q, want immutable caching", cacheControl)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read dashboard script: %v", err)
	}
	if string(body) != dashboard.Script {
		t.Fatal("served dashboard script does not match embedded asset")
	}
}

func TestDashboardVendorAssetsServedLocally(t *testing.T) {
	statlite := New("127.0.0.1:0", nil)
	server := httptest.NewServer(statlite.httpServer.Handler)
	defer server.Close()

	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: dashboard.ChartJSPath, contentType: "application/javascript; charset=utf-8", contains: "Chart.js"},
		{path: dashboard.OrbitronFontPath, contentType: "font/woff2"},
	}
	for _, tt := range tests {
		resp, err := http.Get(server.URL + tt.path)
		if err != nil {
			t.Fatalf("vendor asset request %s: %v", tt.path, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read vendor asset %s: %v", tt.path, readErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("vendor asset %s status = %d, want 200", tt.path, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != tt.contentType {
			t.Errorf("vendor asset %s content-type = %q, want %q", tt.path, got, tt.contentType)
		}
		if len(body) == 0 {
			t.Errorf("vendor asset %s is empty", tt.path)
		}
		if tt.contains != "" && !strings.Contains(string(body), tt.contains) {
			t.Errorf("vendor asset %s missing %q", tt.path, tt.contains)
		}
		if tt.path == dashboard.ChartJSPath && strings.Contains(string(body), "sourceMappingURL") {
			t.Error("vendored Chart.js retains an unserved source map reference")
		}
		if got := resp.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("vendor asset %s cache-control = %q", tt.path, got)
		}
	}
	for _, oldPath := range []string{
		"/static/vendor/chart.umd.min.js",
		"/static/fonts/orbitron-700.woff2",
	} {
		resp, err := http.Get(server.URL + oldPath)
		if err != nil {
			t.Fatalf("old vendor asset request %s: %v", oldPath, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("old vendor asset %s status = %d, want 404", oldPath, resp.StatusCode)
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

func TestHealthzIncludesReadinessOnly(t *testing.T) {
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

	var health map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("decode healthz: %v", err)
	}
	if health["status"] != "ok" || health["timestamp"] == "" {
		t.Fatalf("health response = %#v, want status/timestamp", health)
	}
	if health["version"] != version.Version {
		t.Fatalf("health version = %q, want %q", health["version"], version.Version)
	}
	if _, exists := health["statlite"]; exists {
		t.Fatalf("health response includes legacy metrics payload: %#v", health)
	}
	storageHealth := health["storage"].(map[string]any)
	if storageHealth["status"] != "unavailable" {
		t.Fatalf("storage status = %q, want unavailable", storageHealth["status"])
	}
}

type recordingHostSampler struct {
	metrics  collector.HostMetrics
	warnings []error
	path     string
	calls    int
}

func (s *recordingHostSampler) Sample(path string) (collector.HostMetrics, []error) {
	s.path = path
	s.calls++
	return s.metrics, s.warnings
}

func TestStatliteMetricsEmitsCanonicalProfileAndExcludesScrape(t *testing.T) {
	cpu, memoryUsed, memoryTotal := 0.25, 100.0, 200.0
	diskUsed, diskTotal := 300.0, 400.0
	sampler := &recordingHostSampler{metrics: collector.HostMetrics{
		CPUUsage:         &cpu,
		MemoryUsedBytes:  &memoryUsed,
		MemoryTotalBytes: &memoryTotal,
		DiskUsedBytes:    &diskUsed,
		DiskTotalBytes:   &diskTotal,
	}}
	statlite := NewWithManagerRetentionCutoffAndFilesystem("127.0.0.1:0", nil, 0, nil, "/data/statlite.sqlite")
	statlite.hostSampler = sampler

	normal := httptest.NewRecorder()
	statlite.httpServer.Handler.ServeHTTP(normal, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if normal.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", normal.Code)
	}

	first := httptest.NewRecorder()
	statlite.httpServer.Handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/statlite/metrics", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want 200", first.Code)
	}
	var response statliteMetricsResponse
	if err := json.NewDecoder(first.Body).Decode(&response); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if response.Schema != collector.StatliteMetricsV1Schema || response.Status == "" || response.StartedAt.IsZero() {
		t.Fatalf("profile identity = %#v, want schema/status/started_at", response)
	}
	if response.Metrics.RequestsTotal != 1 {
		t.Fatalf("requests_total = %d, want 1", response.Metrics.RequestsTotal)
	}
	if response.Metrics.RequestDurationSecondsTotal <= 0 || response.Metrics.RequestDurationSecondsMax <= 0 {
		t.Fatalf("request durations = total %v max %v, want positive", response.Metrics.RequestDurationSecondsTotal, response.Metrics.RequestDurationSecondsMax)
	}
	if response.Metrics.RuntimeHeapUsedBytes == 0 || response.Metrics.UptimeSeconds <= 0 || response.Metrics.ProcessCPUUsage < 0 {
		t.Fatalf("runtime metrics = %#v, want available non-negative values", response.Metrics)
	}
	if response.Metrics.HostCPUUsage == nil || *response.Metrics.HostCPUUsage != cpu ||
		response.Metrics.HostMemoryUsedBytes == nil || *response.Metrics.HostMemoryUsedBytes != memoryUsed ||
		response.Metrics.HostDiskTotalBytes == nil || *response.Metrics.HostDiskTotalBytes != diskTotal {
		t.Fatalf("host metrics = %#v, want sampled values", response.Metrics)
	}
	if sampler.path != "/data/statlite.sqlite" {
		t.Fatalf("sample path = %q, want SQLite database path", sampler.path)
	}

	second := httptest.NewRecorder()
	statlite.httpServer.Handler.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/statlite/metrics", nil))
	var secondResponse statliteMetricsResponse
	if err := json.NewDecoder(second.Body).Decode(&secondResponse); err != nil {
		t.Fatalf("decode second metrics response: %v", err)
	}
	if secondResponse.Metrics.RequestsTotal != response.Metrics.RequestsTotal {
		t.Fatalf("metrics scrape changed requests_total from %d to %d", response.Metrics.RequestsTotal, secondResponse.Metrics.RequestsTotal)
	}
	if sampler.calls != 1 {
		t.Fatalf("host sampler calls = %d, want one cached resource sample", sampler.calls)
	}
	if secondResponse.Metrics.RuntimeHeapUsedBytes != response.Metrics.RuntimeHeapUsedBytes ||
		secondResponse.Metrics.ProcessCPUUsage != response.Metrics.ProcessCPUUsage {
		t.Fatalf("process resources changed within cache interval: first %#v second %#v", response.Metrics, secondResponse.Metrics)
	}

	statlite.resourceSampledAt = time.Now().Add(-statlite.resourceInterval)
	third := httptest.NewRecorder()
	statlite.httpServer.Handler.ServeHTTP(third, httptest.NewRequest(http.MethodGet, "/statlite/metrics", nil))
	if sampler.calls != 2 {
		t.Fatalf("host sampler calls after cache expiry = %d, want 2", sampler.calls)
	}
}

func TestStatliteMetricsOmitsUnavailableDisk(t *testing.T) {
	memoryUsed, memoryTotal := 100.0, 200.0
	statlite := New("127.0.0.1:0", nil)
	statlite.hostSampler = &recordingHostSampler{
		metrics: collector.HostMetrics{
			MemoryUsedBytes:  &memoryUsed,
			MemoryTotalBytes: &memoryTotal,
		},
		warnings: []error{fmt.Errorf("host disk usage unavailable")},
	}

	recorder := httptest.NewRecorder()
	statlite.httpServer.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/statlite/metrics", nil))
	var raw map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&raw); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	metrics := raw["metrics"].(map[string]any)
	if _, exists := metrics["host_disk_used_bytes"]; exists {
		t.Fatalf("metrics include unavailable disk used value: %#v", metrics)
	}
	if _, exists := metrics["host_disk_total_bytes"]; exists {
		t.Fatalf("metrics include unavailable disk total value: %#v", metrics)
	}
	if metrics["host_memory_used_bytes"] != memoryUsed {
		t.Fatalf("memory used = %v, want %v", metrics["host_memory_used_bytes"], memoryUsed)
	}
}

func TestResourceSnapshotCoalescesConcurrentRefreshesAndWarnings(t *testing.T) {
	sampler := &recordingHostSampler{
		warnings: []error{fmt.Errorf("sampling failed")},
	}
	statlite := New("127.0.0.1:0", nil)
	statlite.hostSampler = sampler
	now := time.Now().UTC()

	const callers = 20
	warningCounts := make(chan int, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, warnings := statlite.resources(now)
			warningCounts <- len(warnings)
		}()
	}
	wait.Wait()
	close(warningCounts)

	totalWarnings := 0
	for count := range warningCounts {
		totalWarnings += count
	}
	if sampler.calls != 1 {
		t.Fatalf("host sampler calls = %d, want 1", sampler.calls)
	}
	if totalWarnings != 1 {
		t.Fatalf("warnings returned across cached reads = %d, want 1", totalWarnings)
	}
}

func TestResourceSnapshotRefreshReplacesSuccessfulHostDataOnFailure(t *testing.T) {
	memoryUsed := 100.0
	sampler := &recordingHostSampler{
		metrics: collector.HostMetrics{MemoryUsedBytes: &memoryUsed},
	}
	statlite := New("127.0.0.1:0", nil)
	statlite.hostSampler = sampler
	now := time.Now()

	first, warnings := statlite.resources(now)
	if len(warnings) != 0 || first.host.MemoryUsedBytes == nil {
		t.Fatalf("first resources() = %#v, %v; want successful host memory", first, warnings)
	}

	sampler.metrics = collector.HostMetrics{}
	sampler.warnings = []error{fmt.Errorf("host memory unavailable")}
	second, warnings := statlite.resources(now.Add(statlite.resourceInterval))
	if len(warnings) != 1 {
		t.Fatalf("refreshed warnings = %v, want one warning", warnings)
	}
	if second.host.MemoryUsedBytes != nil {
		t.Fatalf("refreshed host memory = %v, want unavailable data to replace cached success", *second.host.MemoryUsedBytes)
	}
}

func TestResourceSnapshotRefreshesAfterClockMovesBackward(t *testing.T) {
	sampler := &recordingHostSampler{}
	statlite := New("127.0.0.1:0", nil)
	statlite.hostSampler = sampler
	now := time.Now()

	statlite.resources(now)
	statlite.resources(now.Add(-time.Second))
	if sampler.calls != 2 {
		t.Fatalf("host sampler calls after backward clock adjustment = %d, want 2", sampler.calls)
	}
}

func TestStatliteMetricsReportsSQLiteDatabaseStatus(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	mon := newServerTestMonitor(t, "statlite-self", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	statlite.hostSampler = &recordingHostSampler{}
	statlite.refreshStorageHealth(t.Context())

	recorder := httptest.NewRecorder()
	statlite.httpServer.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/statlite/metrics", nil))
	var response statliteMetricsResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if response.DatabaseStatus == nil || *response.DatabaseStatus != "UP" {
		t.Fatalf("DatabaseStatus = %v, want UP", response.DatabaseStatus)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() error = %v", err)
	}
	recorder = httptest.NewRecorder()
	statlite.httpServer.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/statlite/metrics", nil))
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode metrics after close: %v", err)
	}
	if response.DatabaseStatus == nil || *response.DatabaseStatus != "DOWN" {
		t.Fatalf("DatabaseStatus after close = %v, want DOWN", response.DatabaseStatus)
	}
}

func TestStorageHealthCheckCachesAndRefreshesWithoutBlockingEndpoints(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	mon := newServerTestMonitor(t, "statlite-self", store, &noopCollector{})
	statlite := New("127.0.0.1:0", mon)
	statlite.hostSampler = &recordingHostSampler{}
	statlite.storageInterval = time.Millisecond
	var healthy atomic.Bool
	healthy.Store(true)
	statlite.storageHealthy = func(context.Context) bool { return healthy.Load() }
	statlite.storageAvailable = func() bool { return true }
	statlite.startStorageHealthChecks()
	defer statlite.stopStorageHealthChecks()

	assertCachedStorageStatus(t, statlite, "ok")
	assertStorageEndpoints(t, statlite, http.StatusOK, "UP")

	healthy.Store(false)
	assertCachedStorageStatus(t, statlite, "error")
	assertStorageEndpoints(t, statlite, http.StatusServiceUnavailable, "DOWN")

	healthy.Store(true)
	assertCachedStorageStatus(t, statlite, "ok")
	assertStorageEndpoints(t, statlite, http.StatusOK, "UP")
}

func assertCachedStorageStatus(t *testing.T, statlite *Server, want string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if statlite.storageHealthStatus() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("storageHealthStatus() = %q, want %q", statlite.storageHealthStatus(), want)
}

func assertStorageEndpoints(t *testing.T, statlite *Server, wantHTTPStatus int, wantDatabaseStatus string) {
	t.Helper()
	health := httptest.NewRecorder()
	statlite.httpServer.Handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != wantHTTPStatus {
		t.Fatalf("healthz status = %d, want %d", health.Code, wantHTTPStatus)
	}

	metrics := httptest.NewRecorder()
	statlite.httpServer.Handler.ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/statlite/metrics", nil))
	var response statliteMetricsResponse
	if err := json.NewDecoder(metrics.Body).Decode(&response); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if response.DatabaseStatus == nil || *response.DatabaseStatus != wantDatabaseStatus {
		t.Fatalf("DatabaseStatus = %v, want %s", response.DatabaseStatus, wantDatabaseStatus)
	}
}

func TestStatliteMetricsProfileIsConsumableByCanonicalCollector(t *testing.T) {
	statlite := New("127.0.0.1:0", nil)
	statlite.hostSampler = &recordingHostSampler{}
	endpoint := httptest.NewServer(statlite.httpServer.Handler)
	defer endpoint.Close()

	client, err := collector.NewStatliteMetricsClient(endpoint.URL+"/statlite/metrics", time.Second)
	if err != nil {
		t.Fatalf("NewStatliteMetricsClient() error = %v", err)
	}
	result, err := collector.NewStatliteMetricsCollector("statlite-self", client).Collect(t.Context())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.HealthStatus != "UP" || result.ProcessStartTime == nil {
		t.Fatalf("collection identity = %#v, want UP and process start time", result)
	}
	for _, sample := range result.Samples {
		if sample.Key == "process_cpu_usage" {
			if sample.Unit != "cores" || sample.Value < 0 {
				t.Fatalf("process CPU sample = %#v, want non-negative cores", sample)
			}
			return
		}
	}
	t.Fatal("collection missing process_cpu_usage sample")
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
	mon, err := monitor.New("app", collector.NewSpringActuatorCollector("app", client, false), store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	statlite.refreshStorageHealth(t.Context())
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
	if health.Storage.Status != "ok" {
		t.Fatalf("storage status = %q, want ok", health.Storage.Status)
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
	mon, err := monitor.New("app", collector.NewSpringActuatorCollector("app", client, false), store, time.Minute)
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
	if health.Storage.Status != "error" {
		t.Fatalf("storage status = %q, want error", health.Storage.Status)
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
		case "/actuator/metrics/jvm.memory.used", "/actuator/metrics/process.cpu.usage", "/actuator/metrics/process.start.time":
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
	mon, err := monitor.New("app", collector.NewSpringActuatorCollector("app", client, false), store, time.Minute)
	if err != nil {
		t.Fatalf("monitor.New() error = %v", err)
	}
	statlite := New("127.0.0.1:0", mon)
	statlite.refreshStorageHealth(t.Context())
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
	if health.Storage.Status != "ok" {
		t.Fatalf("storage status = %q, want ok", health.Storage.Status)
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
	mon, err := monitor.New("app", collector.NewSpringActuatorCollector("app", client, false), store, time.Minute)
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

func TestHandleSeriesSerializesHostResourceFields(t *testing.T) {
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	mon, err := monitor.New("host", &sequenceCollector{results: []collectResult{{result: &collector.CollectionResult{
		TargetName:     "host",
		PollStartedAt:  now,
		PollFinishedAt: now.Add(time.Second),
		HealthStatus:   "UP",
		Samples: []collector.MetricSample{
			{Key: "host_cpu_usage", Kind: collector.MetricKindGauge, Value: 0.25, Unit: "ratio"},
			{Key: "host_memory_used_bytes", Kind: collector.MetricKindGauge, Value: 30, Unit: "bytes"},
			{Key: "host_memory_total_bytes", Kind: collector.MetricKindGauge, Value: 100, Unit: "bytes"},
			{Key: "host_disk_used_bytes", Kind: collector.MetricKindGauge, Value: 40, Unit: "bytes"},
			{Key: "host_disk_total_bytes", Kind: collector.MetricKindGauge, Value: 200, Unit: "bytes"},
		},
	}}}}, store, time.Minute)
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
	var series storage.Series
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(series.Points) != 1 {
		t.Fatalf("series points = %d, want 1", len(series.Points))
	}
	point := series.Points[0]
	assertServerFloatPointer(t, "host_cpu_usage", point.HostCPUUsage, 0.25)
	assertServerFloatPointer(t, "host_memory_used_bytes", point.HostMemoryUsedBytes, 30)
	assertServerFloatPointer(t, "host_memory_total_bytes", point.HostMemoryTotalBytes, 100)
	assertServerFloatPointer(t, "host_memory_usage", point.HostMemoryUsage, 0.3)
	assertServerFloatPointer(t, "host_disk_used_bytes", point.HostDiskUsedBytes, 40)
	assertServerFloatPointer(t, "host_disk_total_bytes", point.HostDiskTotalBytes, 200)
	assertServerFloatPointer(t, "host_disk_usage", point.HostDiskUsage, 0.2)
}

func assertServerFloatPointer(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
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
		{DashboardRange24H, 5 * time.Minute},
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
	// Dense 7d history with 25-minute samples → 30-minute buckets, ~≤336 points.
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
	// More than 336 raw points proves the 30-minute scale bounds the result.
	step := 25 * time.Minute
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
	// Two samples per minute.
	raw := 0
	for at := start; !at.After(end); at = at.Add(30 * time.Second) {
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

func TestParseRangeSupportsRolling24Hours(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/series?range=24h", nil)
	start, end, dashRange, err := parseRange(req)
	if err != nil {
		t.Fatalf("parseRange(24h) error = %v", err)
	}
	if got := end.Sub(start); got != 24*time.Hour {
		t.Fatalf("24h range = %v, want 24h", got)
	}
	if dashRange != DashboardRange24H {
		t.Fatalf("DashboardRange = %q, want %q", dashRange, DashboardRange24H)
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

func TestParseRangeRejectsUnsupportedRanges(t *testing.T) {
	for _, value := range []string{"bad", "today"} {
		req := httptest.NewRequest(http.MethodGet, "/api/series?range="+value, nil)
		_, _, _, err := parseRange(req)
		if err == nil {
			t.Fatalf("parseRange(%s) error = nil, want error", value)
		}
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
