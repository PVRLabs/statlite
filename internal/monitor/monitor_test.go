package monitor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestPollNowTracksFailureStatusAndStoresFailedPoll(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	pollTime := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	mon := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{{
		result: &collector.CollectionResult{
			TargetName:     "app",
			PollStartedAt:  pollTime,
			PollFinishedAt: pollTime.Add(time.Second),
			Events:         []collector.CollectorEvent{{Severity: collector.EventSeverityError, Type: "health_fetch_failed", Message: "unauthorized"}},
		},
		err: errors.New("fetching health: unauthorized"),
	}}})

	snapshot, err := mon.PollNow(context.Background())
	if err == nil {
		t.Fatal("PollNow() error = nil, want collection error")
	}
	if snapshot == nil {
		t.Fatal("PollNow() snapshot = nil, want stored failed snapshot")
	}
	if snapshot.Status != "error" {
		t.Fatalf("snapshot Status = %q, want error", snapshot.Status)
	}
	if latest := mon.LatestSnapshot(); latest == nil || latest.PollID != snapshot.PollID {
		t.Fatalf("LatestSnapshot() = %#v, want failed poll %d", latest, snapshot.PollID)
	}
	status := mon.Status()
	if status.ConsecutivePollFailures != 1 {
		t.Fatalf("ConsecutivePollFailures = %d, want 1", status.ConsecutivePollFailures)
	}
	if status.LastFailedPollAt == nil {
		t.Fatal("LastFailedPollAt = nil, want timestamp")
	}
}

func TestPollNowDetectsRestartWhenProcessStartTimeChanges(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	firstStart := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	secondStart := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	mon := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{
		{result: successfulResult(firstStart, 100, 10)},
		{result: successfulResult(secondStart, 2, 0.2)},
	}})

	first, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("first PollNow() error = %v", err)
	}
	second, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("second PollNow() error = %v", err)
	}
	if first.AppRunID == nil || second.AppRunID == nil {
		t.Fatalf("app run ids = %v, %v; want both set", first.AppRunID, second.AppRunID)
	}
	if *first.AppRunID == *second.AppRunID {
		t.Fatalf("app run id did not change after process start changed: %d", *first.AppRunID)
	}
	if !hasEvent(second.Result.Events, EventTypeRestartDetected) {
		t.Fatalf("events = %#v, want %s", second.Result.Events, EventTypeRestartDetected)
	}
}

func TestPollNowDetectsRestartAfterMonitorRecreation(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	firstStart := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	first := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{
		{result: successfulResult(firstStart, 100, 10)},
	}})
	firstSnapshot, err := first.PollNow(context.Background())
	if err != nil {
		t.Fatalf("first PollNow() error = %v", err)
	}

	secondStart := firstStart.Add(time.Hour)
	recreated := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{
		{result: successfulResult(secondStart, 2, 0.2)},
	}})
	secondSnapshot, err := recreated.PollNow(context.Background())
	if err != nil {
		t.Fatalf("recreated PollNow() error = %v", err)
	}

	if firstSnapshot.AppRunID == nil || secondSnapshot.AppRunID == nil {
		t.Fatalf("app run ids = %v, %v; want both set", firstSnapshot.AppRunID, secondSnapshot.AppRunID)
	}
	if *firstSnapshot.AppRunID == *secondSnapshot.AppRunID {
		t.Fatalf("recreated monitor kept app run %d after process start changed", *secondSnapshot.AppRunID)
	}
	if !hasEvent(secondSnapshot.Result.Events, EventTypeRestartDetected) {
		t.Fatalf("events = %#v, want %s", secondSnapshot.Result.Events, EventTypeRestartDetected)
	}
}

func TestPollNowDetectsUptimeRestartAfterMonitorRecreation(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	firstAt := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	secondAt := firstAt.Add(time.Minute)
	first := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{
		{result: uptimeResult(firstAt, 3600)},
	}})
	firstSnapshot, err := first.PollNow(context.Background())
	if err != nil {
		t.Fatalf("first PollNow() error = %v", err)
	}

	recreated := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{
		{result: uptimeResult(secondAt, 30)},
	}})
	secondSnapshot, err := recreated.PollNow(context.Background())
	if err != nil {
		t.Fatalf("recreated PollNow() error = %v", err)
	}

	if firstSnapshot.AppRunID == nil || secondSnapshot.AppRunID == nil {
		t.Fatalf("app run ids = %v, %v; want both set", firstSnapshot.AppRunID, secondSnapshot.AppRunID)
	}
	if *firstSnapshot.AppRunID == *secondSnapshot.AppRunID {
		t.Fatalf("recreated monitor kept app run %d after uptime decreased", *secondSnapshot.AppRunID)
	}
	if !hasEvent(secondSnapshot.Result.Events, EventTypeRestartDetected) {
		t.Fatalf("events = %#v, want %s", secondSnapshot.Result.Events, EventTypeRestartDetected)
	}
}

func TestPollNowRejectsNilCollectorResult(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	mon := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{{}}})
	snapshot, err := mon.PollNow(context.Background())
	if err == nil || !strings.Contains(err.Error(), "collector returned no result") {
		t.Fatalf("PollNow() error = %v, want missing result error", err)
	}
	if snapshot == nil || snapshot.Status != "error" {
		t.Fatalf("PollNow() snapshot = %#v, want stored error poll", snapshot)
	}
	if !hasEvent(snapshot.Result.Events, "collector_failed") {
		t.Fatalf("events = %#v, want collector_failed", snapshot.Result.Events)
	}
}

func TestStatliteMetricsStartedAtDrivesRestartAwareSeries(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	metricCollector := newStatliteMetricsSequenceCollector(t, []string{
		`{
			"schema":"statlite-metrics/v1",
			"status":"UP",
			"started_at":"2026-07-27T19:00:00Z",
			"metrics":{"requests_total":100,"request_duration_seconds_total":20}
		}`,
		`{
			"schema":"statlite-metrics/v1",
			"status":"UP",
			"started_at":"2026-07-27T19:00:00Z",
			"metrics":{"requests_total":120,"request_duration_seconds_total":24}
		}`,
		`{
			"schema":"statlite-metrics/v1",
			"status":"UP",
			"started_at":"2026-07-27T20:00:00Z",
			"metrics":{"requests_total":5,"request_duration_seconds_total":1}
		}`,
	})
	mon := newTestMonitor(t, store, metricCollector)
	seriesStart := time.Now().UTC().Add(-time.Second)

	first, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("first PollNow() error = %v", err)
	}
	second, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("second PollNow() error = %v", err)
	}
	third, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("third PollNow() error = %v", err)
	}

	if first.AppRunID == nil || second.AppRunID == nil || third.AppRunID == nil {
		t.Fatalf("app run ids = %v, %v, %v; want all set", first.AppRunID, second.AppRunID, third.AppRunID)
	}
	if *first.AppRunID != *second.AppRunID {
		t.Fatalf("unchanged started_at changed app run: %d -> %d", *first.AppRunID, *second.AppRunID)
	}
	if *second.AppRunID == *third.AppRunID {
		t.Fatalf("changed started_at kept app run %d", *third.AppRunID)
	}
	if hasEvent(second.Result.Events, EventTypeRestartDetected) {
		t.Fatalf("second events = %#v, did not want restart", second.Result.Events)
	}
	if !hasEvent(third.Result.Events, EventTypeRestartDetected) {
		t.Fatalf("third events = %#v, want restart", third.Result.Events)
	}

	series, err := mon.Series(context.Background(), seriesStart, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 3 {
		t.Fatalf("series points len = %d, want 3", len(series.Points))
	}
	if series.Points[0].Requests != nil || series.Points[0].AverageLatencySeconds != nil {
		t.Fatalf("first point deltas = %v/%v, want nil/nil", series.Points[0].Requests, series.Points[0].AverageLatencySeconds)
	}
	assertMonitorFloatPointer(t, "same-run requests", series.Points[1].Requests, 20)
	assertMonitorFloatPointer(t, "same-run latency", series.Points[1].AverageLatencySeconds, 0.2)
	if series.Points[2].Requests != nil || series.Points[2].AverageLatencySeconds != nil {
		t.Fatalf("restart point deltas = %v/%v, want nil/nil", series.Points[2].Requests, series.Points[2].AverageLatencySeconds)
	}
}

func TestStatliteMetricsMissingAndInvalidStartedAtRemainUsable(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	metricCollector := newStatliteMetricsSequenceCollector(t, []string{
		`{
			"schema":"statlite-metrics/v1",
			"status":"UP",
			"metrics":{"requests_total":10,"request_duration_seconds_total":2}
		}`,
		`{
			"schema":"statlite-metrics/v1",
			"status":"UP",
			"started_at":"not-rfc3339",
			"metrics":{"requests_total":15,"request_duration_seconds_total":3}
		}`,
	})
	mon := newTestMonitor(t, store, metricCollector)
	seriesStart := time.Now().UTC().Add(-time.Second)

	first, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("first PollNow() error = %v", err)
	}
	second, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("second PollNow() error = %v", err)
	}
	if first.Status != "ok" || second.Status != "ok" {
		t.Fatalf("poll statuses = %q/%q, want ok/ok", first.Status, second.Status)
	}
	if first.Result.ProcessStartTime != nil || second.Result.ProcessStartTime != nil {
		t.Fatalf("process start times = %v/%v, want nil/nil", first.Result.ProcessStartTime, second.Result.ProcessStartTime)
	}
	if !hasEvent(first.Result.Events, "process_start_time_missing") {
		t.Fatalf("first events = %#v, want missing started_at warning", first.Result.Events)
	}
	if !hasEvent(second.Result.Events, "process_start_time_invalid") {
		t.Fatalf("second events = %#v, want invalid started_at warning", second.Result.Events)
	}
	if first.AppRunID == nil || second.AppRunID == nil || *first.AppRunID != *second.AppRunID {
		t.Fatalf("app run ids = %v/%v, want same anonymous run", first.AppRunID, second.AppRunID)
	}
	if hasEvent(second.Result.Events, EventTypeRestartDetected) {
		t.Fatalf("second events = %#v, did not want restart for increasing counters", second.Result.Events)
	}

	series, err := mon.Series(context.Background(), seriesStart, time.Now().UTC().Add(time.Second))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 2 {
		t.Fatalf("series points len = %d, want 2", len(series.Points))
	}
	assertMonitorFloatPointer(t, "anonymous-run requests", series.Points[1].Requests, 5)
	assertMonitorFloatPointer(t, "anonymous-run latency", series.Points[1].AverageLatencySeconds, 0.2)
}

func TestPollNowDoesNotRestartOnOneCoreCounterDecreaseWithoutFailure(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	start := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	mon := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{
		{result: successfulResult(start, 100, 10)},
		{result: successfulResult(start, 90, 11)},
	}})

	first, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("first PollNow() error = %v", err)
	}
	second, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("second PollNow() error = %v", err)
	}
	if first.AppRunID == nil || second.AppRunID == nil {
		t.Fatalf("app run ids = %v, %v; want both set", first.AppRunID, second.AppRunID)
	}
	if *first.AppRunID != *second.AppRunID {
		t.Fatalf("app run id changed on one counter decrease: %d -> %d", *first.AppRunID, *second.AppRunID)
	}
	if hasEvent(second.Result.Events, EventTypeRestartDetected) {
		t.Fatalf("events = %#v, did not want %s", second.Result.Events, EventTypeRestartDetected)
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
	result := c.results[c.index]
	c.index++
	return result.result, result.err
}

func newTestMonitor(t *testing.T, store *storage.Store, collector Collector) *Monitor {
	t.Helper()
	mon, err := New("app", collector, store, time.Minute)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return mon
}

func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	store, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "statlite.sqlite"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	return store
}

func successfulResult(processStart time.Time, requests, requestSeconds float64) *collector.CollectionResult {
	pollStarted := processStart.Add(time.Hour)
	return &collector.CollectionResult{
		TargetName:       "app",
		PollStartedAt:    pollStarted,
		PollFinishedAt:   pollStarted.Add(time.Second),
		HealthStatus:     "UP",
		ProcessStartTime: &processStart,
		Samples: []collector.MetricSample{
			{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: requests, Unit: "requests"},
			{Key: "http_request_time_total_seconds", Kind: collector.MetricKindCounter, Value: requestSeconds, Unit: "seconds"},
		},
	}
}

func uptimeResult(at time.Time, uptime float64) *collector.CollectionResult {
	return &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  at,
		PollFinishedAt: at.Add(time.Second),
		HealthStatus:   "UP",
		Samples: []collector.MetricSample{
			{Key: "process_uptime", Kind: collector.MetricKindGauge, Value: uptime, Unit: "seconds"},
		},
	}
}

func hasEvent(events []collector.CollectorEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func newStatliteMetricsSequenceCollector(t *testing.T, bodies []string) *collector.StatliteMetricsCollector {
	t.Helper()
	var mu sync.Mutex
	next := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if next >= len(bodies) {
			http.Error(w, "no response configured", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(bodies[next]))
		next++
	}))
	t.Cleanup(server.Close)

	client, err := collector.NewStatliteMetricsClient(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewStatliteMetricsClient() error = %v", err)
	}
	return collector.NewStatliteMetricsCollector("app", client)
}

func assertMonitorFloatPointer(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}
