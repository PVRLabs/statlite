package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

func publicEventsRequest(t *testing.T, server *Server, path string) map[string]any {
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

func TestPublicEventsShapeWindowAndTarget(t *testing.T) {
	store := newPublicTestStore(t)
	appCollector := &countingCollector{}
	alpha := newServerTestMonitor(t, "alpha", store, appCollector)
	beta := newServerTestMonitor(t, "beta", store, &countingCollector{})
	server := NewWithManager("", newServerTestManager(t, alpha, beta))
	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	recent := now.Add(-2 * time.Minute)
	latest := now.Add(-time.Minute)
	for _, entry := range []struct {
		target string
		at     time.Time
		event  collector.CollectorEvent
	}{
		{"alpha", old, collector.CollectorEvent{Severity: collector.EventSeverityWarning, Type: "old_type", Message: "old"}},
		{"alpha", recent, collector.CollectorEvent{Severity: collector.EventSeverityWarning, Type: "missing_metric", MetricKey: "process_cpu_usage", Message: "missing"}},
		{"alpha", latest, collector.CollectorEvent{Severity: collector.EventSeverityError, Type: "fetch_failed", Message: "failed"}},
		{"beta", latest, collector.CollectorEvent{Severity: collector.EventSeverityError, Type: "other_target", Message: "private"}},
	} {
		result := serverTestResult(entry.target, entry.at, 1, 1, []collector.CollectorEvent{entry.event})
		if _, err := store.SaveCollectionResult(t.Context(), result); err != nil {
			t.Fatal(err)
		}
	}

	body := publicEventsRequest(t, server, "/api/v1/events?target=alpha")
	if body["target"] != "alpha" || len(body) != 2 {
		t.Fatalf("response wrapper = %#v", body)
	}
	events, ok := body["events"].([]any)
	if !ok || len(events) != 2 {
		t.Fatalf("events = %#v", body["events"])
	}
	first := events[0].(map[string]any)
	second := events[1].(map[string]any)
	if first["timestamp"] != latest.Format(time.RFC3339Nano) || first["severity"] != "error" || first["type"] != "fetch_failed" || first["message"] != "failed" {
		t.Fatalf("latest event = %#v", first)
	}
	if second["timestamp"] != recent.Format(time.RFC3339Nano) || second["severity"] != "warning" || second["type"] != "missing_metric" || second["message"] != "missing" || second["metric_key"] != "process_cpu_usage" {
		t.Fatalf("older event = %#v", second)
	}
	if _, exists := first["metric_key"]; exists {
		t.Fatalf("metric_key should be omitted: %#v", first)
	}
	for _, event := range []map[string]any{first, second} {
		for _, forbidden := range []string{"target", "poll_id", "event_id", "id", "endpoint"} {
			if _, exists := event[forbidden]; exists {
				t.Errorf("event exposes %q: %#v", forbidden, event)
			}
		}
	}
	if len(body) != 2 {
		t.Fatalf("response has unexpected fields: %#v", body)
	}
	if got := publicEventsRequest(t, server, "/api/v1/events?target=alpha&range=5m"); len(got["events"].([]any)) != 2 {
		t.Fatalf("5m events = %#v", got)
	}
	if got := publicEventsRequest(t, server, "/api/v1/events?target=alpha&range=24h"); len(got["events"].([]any)) != 3 {
		t.Fatalf("24h events = %#v", got)
	}
	if got := publicEventsRequest(t, server, "/api/v1/events?target=beta"); len(got["events"].([]any)) != 1 {
		t.Fatalf("beta events = %#v", got)
	}
	if appCollector.calls.Load() != 0 {
		t.Fatalf("event requests triggered %d collections", appCollector.calls.Load())
	}
}

func TestPublicEventsBoundsAndEmptyResults(t *testing.T) {
	store := newPublicTestStore(t)
	mon := newServerTestMonitor(t, "app", store, &countingCollector{})
	server := NewWithManager("", mustSingleServerTestManager(t, mon))
	if body := publicEventsRequest(t, server, "/api/v1/events"); body["target"] != "app" || len(body["events"].([]any)) != 0 {
		t.Fatalf("empty events = %#v", body)
	}

	at := time.Now().UTC().Add(-time.Minute)
	events := make([]collector.CollectorEvent, 510)
	for i := range events {
		events[i] = collector.CollectorEvent{Severity: collector.EventSeverityWarning, Type: "diagnostic", Message: fmt.Sprintf("event-%03d", i)}
	}
	if _, err := store.SaveCollectionResult(t.Context(), serverTestResult("app", at, 1, 1, events)); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path        string
		wantCount   int
		lastMessage string
	}{
		{"/api/v1/events", 100, "event-410"},
		{"/api/v1/events?limit=500", 500, "event-010"},
		{"/api/v1/events?limit=1", 1, "event-509"},
	} {
		body := publicEventsRequest(t, server, tt.path)
		got := body["events"].([]any)
		if len(got) != tt.wantCount || got[0].(map[string]any)["message"] != "event-509" || got[len(got)-1].(map[string]any)["message"] != tt.lastMessage {
			t.Fatalf("GET %s returned %d events, first %#v, last %#v", tt.path, len(got), got[0], got[len(got)-1])
		}
	}
	server.retentionDays = 1
	server.retentionCutoff = func() time.Time { return time.Now().UTC().Add(time.Minute) }
	if body := publicEventsRequest(t, server, "/api/v1/events"); body["target"] != "app" || len(body["events"].([]any)) != 0 {
		t.Fatalf("expired window = %#v", body)
	}
	server.retentionCutoff = func() time.Time { return at.Add(time.Nanosecond) }
	if body := publicEventsRequest(t, server, "/api/v1/events"); len(body["events"].([]any)) != 0 {
		t.Fatalf("retention cutoff = %#v", body)
	}
}

func TestPublicEventsRejectsInvalidRequests(t *testing.T) {
	store := newPublicTestStore(t)
	alpha := newServerTestMonitor(t, "alpha", store, &countingCollector{})
	beta := newServerTestMonitor(t, "beta", store, &countingCollector{})
	server := NewWithManager("", newServerTestManager(t, alpha, beta))
	for _, path := range []string{
		"/api/v1/events", "/api/v1/events?target=missing", "/api/v1/events?target=",
		"/api/v1/events?target=alpha&target=alpha", "/api/v1/events?target=alpha&rang=5m",
		"/api/v1/events?target=alpha&range=", "/api/v1/events?target=alpha&range=7d",
		"/api/v1/events?target=alpha&range=5m&range=5m", "/api/v1/events?target=alpha&limit=0",
		"/api/v1/events?target=alpha&limit=501", "/api/v1/events?target=alpha&limit=1.5",
		"/api/v1/events?target=alpha&limit=", "/api/v1/events?target=alpha&limit=1&limit=1",
		"/api/v1/events?target=alpha&start=2026-01-01",
	} {
		response := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/events?target=alpha", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("POST events = %d, Allow %q", response.Code, response.Header().Get("Allow"))
	}
}

func TestPublicEventsHidesTransportEndpointWithoutChangingStoredDiagnostic(t *testing.T) {
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
	stored, err := store.Events(t.Context(), "app", time.Now().Add(-time.Hour), time.Now().Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || !strings.Contains(stored[0].Message, endpoint) {
		t.Fatalf("stored transport diagnostic missing endpoint: %#v", stored)
	}
	body := publicEventsRequest(t, server, "/api/v1/events")
	events := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("public events = %#v", body)
	}
	event := events[0].(map[string]any)
	if event["type"] != stored[0].Type || event["message"] != "collection request failed" {
		t.Fatalf("public transport event = %#v", event)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), endpoint) || strings.Contains(string(encoded), remote.URL) || strings.Contains(string(encoded), "secret") {
		t.Fatalf("public events leaked endpoint: %s", encoded)
	}
}

func TestPublicEventsDuringPolling(t *testing.T) {
	store := newPublicTestStore(t)
	base := time.Now().UTC().Add(-time.Minute)
	const polls = 20
	results := make([]collectResult, polls)
	for i := range results {
		results[i].result = serverTestResult("app", base.Add(time.Duration(i)*time.Second), float64(i+1), float64(i+1), []collector.CollectorEvent{{Severity: collector.EventSeverityWarning, Type: "diagnostic", Message: "poll"}})
	}
	collector := &sequenceCollector{results: results}
	mon := newServerTestMonitor(t, "app", store, collector)
	server := NewWithManager("", mustSingleServerTestManager(t, mon))
	var wg sync.WaitGroup
	wg.Add(1)
	pollErrors := make(chan error, 1)
	go func() {
		defer wg.Done()
		for i := 0; i < polls; i++ {
			if _, err := mon.PollNow(t.Context()); err != nil {
				pollErrors <- err
				return
			}
		}
	}()
	for i := 0; i < polls; i++ {
		body := publicEventsRequest(t, server, "/api/v1/events")
		if body["target"] != "app" || body["events"] == nil {
			t.Fatalf("events during polling = %#v", body)
		}
		status := publicStatusRequest(t, server, "/api/v1/status")
		if status["target"] != "app" {
			t.Fatalf("status during polling = %#v", status)
		}
	}
	wg.Wait()
	select {
	case err := <-pollErrors:
		t.Fatal(err)
	default:
	}
	if collector.index != polls {
		t.Fatalf("poll count = %d, want %d; requests must not collect", collector.index, polls)
	}
}
