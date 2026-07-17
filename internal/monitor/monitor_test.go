package monitor

import (
	"context"
	"errors"
	"path/filepath"
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

func hasEvent(events []collector.CollectorEvent, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
