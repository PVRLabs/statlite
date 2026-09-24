package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
)

func newPublicTestStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(t.Context(), t.TempDir()+"/statlite.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	registerServerTestTargets(t, store)
	return store
}

func publicStatusRequest(t *testing.T, server *Server, path string) map[string]any {
	t.Helper()
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestPublicStatusNoPollAndRequestBoundary(t *testing.T) {
	store := newPublicTestStore(t)
	collector := &countingCollector{}
	mon := newServerTestMonitor(t, "app", store, collector)
	server := NewWithManager("", mustSingleServerTestManager(t, mon))
	body := publicStatusRequest(t, server, "/api/v1/status")
	if body["target"] != "app" || body["type"] != "spring" || body["collection_status"] != "not_polled" || body["consecutive_collection_failures"] != float64(0) {
		t.Fatalf("no-poll identity/status = %#v", body)
	}
	for _, key := range []string{"last_poll_at", "last_successful_poll_at", "last_failed_poll_at", "last_collection_error", "application_health", "dependency_health", "health_observed_at"} {
		if value, exists := body[key]; !exists || value != nil {
			t.Errorf("%s = %#v, want explicit null", key, value)
		}
	}
	for _, forbidden := range []string{"monitor", "latest", "endpoint", "endpoint_source", "poll_id", "last_stored_poll_id", "last_successful_stored_poll_id", "healthy"} {
		if _, exists := body[forbidden]; exists {
			t.Errorf("unexpected field %q", forbidden)
		}
	}
	for _, path := range []string{
		"/api/v1/status?target=missing", "/api/v1/status?target=", "/api/v1/status?target=app&target=app",
		"/api/v1/status?rang=5m", "/api/v1/status?range=1h",
	} {
		response := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/status", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST status = %d, Allow %q", response.Code, response.Header().Get("Allow"))
	}
	if collector.calls.Load() != 0 {
		t.Fatalf("status requests collected %d times", collector.calls.Load())
	}
}

func TestPublicStatusPollHealthAndFailures(t *testing.T) {
	store := newPublicTestStore(t)
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	result := func(at time.Time, app, db string) *collector.CollectionResult {
		return &collector.CollectionResult{
			TargetName: "app", PollStartedAt: at, PollFinishedAt: at.Add(time.Second),
			HealthStatus: app, DBHealthStatus: db,
			Samples: []collector.MetricSample{{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: 1}},
		}
	}
	failed := func(at time.Time, app, db string) collectResult {
		return collectResult{result: result(at, app, db), err: errors.New("collection failed")}
	}
	mon := newServerTestMonitor(t, "app", store, &sequenceCollector{results: []collectResult{
		{result: result(base, "UP", "DOWN")},
		failed(base.Add(time.Minute), "", ""),
		failed(base.Add(2*time.Minute), "OUT_OF_SERVICE", ""),
		failed(base.Add(3*time.Minute), "", ""),
	}})
	server := NewWithManager("", mustSingleServerTestManager(t, mon))
	for i, want := range []struct {
		collection, app, db string
		failures            float64
		healthAt            time.Time
	}{
		{"ok", "UP", "DOWN", 0, base.Add(time.Second)},
		{"error", "UP", "DOWN", 1, base.Add(time.Second)},
		{"error", "OUT_OF_SERVICE", "", 2, base.Add(2*time.Minute + time.Second)},
		{"error", "UP", "DOWN", 3, base.Add(time.Second)},
	} {
		if _, err := mon.PollNow(t.Context()); (err != nil) != (i > 0) {
			t.Fatalf("poll %d error = %v", i, err)
		}
		body := publicStatusRequest(t, server, "/api/v1/status?target=app")
		if body["collection_status"] != want.collection || body["consecutive_collection_failures"] != want.failures || body["application_health"] != want.app || body["health_observed_at"] != want.healthAt.Format(time.RFC3339) {
			t.Fatalf("poll %d status = %#v", i, body)
		}
		if want.db == "" {
			if body["dependency_health"] != nil {
				t.Fatalf("poll %d dependency health = %#v, want null", i, body["dependency_health"])
			}
		} else if body["dependency_health"] != want.db {
			t.Fatalf("poll %d dependency health = %#v", i, body["dependency_health"])
		}
		if body["last_poll_at"] != base.Add(time.Duration(i)*time.Minute+time.Second).Format(time.RFC3339) || body["last_successful_poll_at"] != base.Add(time.Second).Format(time.RFC3339) {
			t.Fatalf("poll %d times = %#v", i, body)
		}
		if i == 0 {
			if body["last_failed_poll_at"] != nil || body["last_collection_error"] != nil {
				t.Fatalf("successful poll has failure fields: %#v", body)
			}
		} else if body["last_failed_poll_at"] != body["last_poll_at"] || !strings.Contains(body["last_collection_error"].(string), "collection failed") {
			t.Fatalf("poll %d failure fields = %#v", i, body)
		}
	}
}

func TestPublicStatusPreStorageFailureUsesSuccessfulHealth(t *testing.T) {
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	success := &storage.Snapshot{PollID: 1, Result: collector.CollectionResult{HealthStatus: "UP", DBHealthStatus: "UP", PollFinishedAt: base}}
	latestAt := base.Add(2 * time.Minute)
	status := monitor.Status{LastPollAt: &latestAt, LastStoredPollID: 0, ConsecutivePollFailures: 2, LastPollErrorSummary: "store poll: unavailable"}
	response := publicStatus("app", "spring", status, success, monitor.HealthObservation{})
	if response.CollectionStatus != "error" || response.ApplicationHealth == nil || *response.ApplicationHealth != "UP" || response.HealthObservedAt == nil || !response.HealthObservedAt.Equal(base) || response.LastSuccessfulPollAt == nil || !response.LastSuccessfulPollAt.Equal(base) {
		t.Fatalf("pre-storage failure status = %#v", response)
	}
}

func TestPublicStatusNoPollRestoresFailedCollectionAndHealth(t *testing.T) {
	store := newPublicTestStore(t)
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	good := serverTestResult("app", base, 1, 1, nil)
	good.DBHealthStatus = "DOWN"
	if _, err := store.SaveCollectionResult(t.Context(), good); err != nil {
		t.Fatal(err)
	}
	bad := &collector.CollectionResult{
		TargetName: "app", PollStartedAt: base.Add(time.Minute), PollFinishedAt: base.Add(time.Minute + time.Second),
		Events: []collector.CollectorEvent{{Severity: collector.EventSeverityError, Type: "collector_failed", Message: "offline"}},
	}
	if _, err := store.SaveCollectionResult(t.Context(), bad); err != nil {
		t.Fatal(err)
	}
	mon := newServerTestMonitor(t, "app", store, &countingCollector{})
	if err := mon.EnableNoPoll(t.Context()); err != nil {
		t.Fatal(err)
	}
	body := publicStatusRequest(t, NewWithManager("", mustSingleServerTestManager(t, mon)), "/api/v1/status")
	if body["collection_status"] != "error" || body["consecutive_collection_failures"] != float64(1) || body["application_health"] != "UP" || body["dependency_health"] != "DOWN" || body["health_observed_at"] != base.Add(time.Second).Format(time.RFC3339) {
		t.Fatalf("restored status = %#v", body)
	}
	if body["last_successful_poll_at"] != base.Add(time.Second).Format(time.RFC3339) || body["last_failed_poll_at"] != base.Add(time.Minute+time.Second).Format(time.RFC3339) || body["last_collection_error"] == nil {
		t.Fatalf("restored times/error = %#v", body)
	}
}

func TestPublicStatusFirstFailedPollCanReportHealth(t *testing.T) {
	store := newPublicTestStore(t)
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	result := &collector.CollectionResult{
		TargetName: "app", PollStartedAt: base, PollFinishedAt: base.Add(time.Second), HealthStatus: "DOWN",
		Events: []collector.CollectorEvent{{Severity: collector.EventSeverityError, Type: "collector_failed", Message: "metrics unavailable"}},
	}
	mon := newServerTestMonitor(t, "app", store, &sequenceCollector{results: []collectResult{{result: result, err: errors.New("metrics unavailable")}}})
	if _, err := mon.PollNow(t.Context()); err == nil {
		t.Fatal("failed poll returned no error")
	}
	body := publicStatusRequest(t, NewWithManager("", mustSingleServerTestManager(t, mon)), "/api/v1/status")
	if body["collection_status"] != "error" || body["application_health"] != "DOWN" || body["dependency_health"] != nil || body["last_successful_poll_at"] != nil || body["health_observed_at"] != base.Add(time.Second).Format(time.RFC3339) {
		t.Fatalf("first failed poll status = %#v", body)
	}
}

func TestPublicStatusKeepsNewHealthWhenStorageWriteFails(t *testing.T) {
	store := newPublicTestStore(t)
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	good := serverTestResult("app", base, 1, 1, nil)
	good.DBHealthStatus = "UP"
	newHealth := serverTestResult("app", base.Add(time.Minute), 2, 2, nil)
	newHealth.HealthStatus = "DOWN"
	newHealth.DBHealthStatus = ""
	mon := newServerTestMonitor(t, "app", store, &sequenceCollector{results: []collectResult{{result: good}, {result: newHealth}}})
	if _, err := mon.PollNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := mon.PollNow(t.Context()); err == nil {
		t.Fatal("poll after storage close returned no error")
	}
	body := publicStatusRequest(t, NewWithManager("", mustSingleServerTestManager(t, mon)), "/api/v1/status")
	if body["collection_status"] != "error" || body["application_health"] != "DOWN" || body["dependency_health"] != nil || body["health_observed_at"] != newHealth.PollFinishedAt.Format(time.RFC3339) {
		t.Fatalf("new health after storage failure = %#v", body)
	}
	if body["last_successful_poll_at"] != good.PollFinishedAt.Format(time.RFC3339) || body["last_failed_poll_at"] != newHealth.PollFinishedAt.Format(time.RFC3339) || body["consecutive_collection_failures"] != float64(1) {
		t.Fatalf("storage failure state = %#v", body)
	}
}

func TestPublicStatusCountsHistoryReadFailuresAsPollAttempts(t *testing.T) {
	store := newPublicTestStore(t)
	collector := &countingCollector{}
	mon := newServerTestMonitor(t, "app", store, collector)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	server := NewWithManager("", mustSingleServerTestManager(t, mon))
	for failures := 1; failures <= 2; failures++ {
		if _, err := mon.PollNow(t.Context()); err == nil {
			t.Fatal("history read failure returned no error")
		}
		body := publicStatusRequest(t, server, "/api/v1/status")
		if body["collection_status"] != "error" || body["consecutive_collection_failures"] != float64(failures) || body["last_poll_at"] == nil || body["last_failed_poll_at"] != body["last_poll_at"] || body["last_collection_error"] == nil {
			t.Fatalf("history read failure %d = %#v", failures, body)
		}
		if body["last_successful_poll_at"] != nil || body["application_health"] != nil || body["dependency_health"] != nil {
			t.Fatalf("history read failure %d invented prior state: %#v", failures, body)
		}
	}
	if collector.calls.Load() != 0 {
		t.Fatalf("collector called %d times after history failure", collector.calls.Load())
	}
}

func TestPublicStatusLegacyServerInfersCanonicalTypeAndHidesTransportURL(t *testing.T) {
	store := newPublicTestStore(t)
	remote := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := remote.URL + "/private/metrics?token=secret"
	remote.Close()
	client, err := collector.NewStatliteMetricsClient(endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	mon := newServerTestMonitor(t, "app", store, collector.NewStatliteMetricsCollector("app", client))
	server := New("", mon)
	if _, err := mon.PollNow(t.Context()); err == nil {
		t.Fatal("transport failure returned no error")
	}
	if !strings.Contains(mon.Status().LastPollErrorSummary, endpoint) {
		t.Fatalf("test transport failure did not include endpoint: %q", mon.Status().LastPollErrorSummary)
	}
	body := publicStatusRequest(t, server, "/api/v1/status")
	if body["type"] != "statlite-metrics" || body["collection_status"] != "error" || body["last_collection_error"] != "collection request failed" {
		t.Fatalf("legacy server status = %#v", body)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), endpoint) || strings.Contains(string(encoded), remote.URL) || strings.Contains(string(encoded), "secret") {
		t.Fatalf("public response leaked endpoint: %s", encoded)
	}
}

func TestPublicStatusRejectsUnknownLegacyCollectorType(t *testing.T) {
	store := newPublicTestStore(t)
	mon := newServerTestMonitor(t, "app", store, &countingCollector{})
	response := httptest.NewRecorder()
	New("", mon).httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("unknown collector type status = %d, want 500", response.Code)
	}
}

func TestPublicStatusLegacyServerUsesCanonicalBuiltInTypes(t *testing.T) {
	store := newPublicTestStore(t)
	for _, tt := range []struct {
		name      string
		collector monitor.Collector
		wantType  string
	}{
		{"spring", collector.NewSpringActuatorCollector("app", nil, false), "spring"},
		{"quarkus", collector.NewQuarkusCollector("app", "", nil, nil), "quarkus"},
		{"statlite metrics", collector.NewStatliteMetricsCollector("app", nil), "statlite-metrics"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mon := newServerTestMonitor(t, "app", store, tt.collector)
			body := publicStatusRequest(t, New("", mon), "/api/v1/status")
			if body["type"] != tt.wantType {
				t.Fatalf("legacy server type = %#v, want %q", body["type"], tt.wantType)
			}
		})
	}
}

func TestPublicStatusRequiresExactTargetWithMultipleConfigured(t *testing.T) {
	store := newPublicTestStore(t)
	alpha := newServerTestMonitor(t, "alpha", store, &countingCollector{})
	beta := newServerTestMonitor(t, "beta", store, &countingCollector{})
	server := NewWithManager("", newServerTestManager(t, alpha, beta))
	for _, path := range []string{"/api/v1/status", "/api/v1/status?target=missing"} {
		response := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("GET %s = %d, want 400", path, response.Code)
		}
	}
	if body := publicStatusRequest(t, server, "/api/v1/status?target=beta"); body["target"] != "beta" || body["type"] != "statlite" {
		t.Fatalf("selected target = %#v", body)
	}
}
