package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

func TestOpenCreatesSchema(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	for _, table := range []string{"targets", "polls", "app_runs", "metric_samples", "collector_events"} {
		var name string
		err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("schema table %s missing: %v", table, err)
		}
	}
}

func TestSaveCollectionResultAndLatestSnapshot(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	processStart := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	result := &collector.CollectionResult{
		TargetName:       "app",
		PollStartedAt:    time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC),
		PollFinishedAt:   time.Date(2026, 7, 7, 10, 1, 1, 0, time.UTC),
		HealthStatus:     "UP",
		DBHealthStatus:   "UP",
		ProcessStartTime: &processStart,
		Samples: []collector.MetricSample{
			{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: 42, Unit: "requests"},
			{Key: "process_cpu_usage", Kind: collector.MetricKindGauge, Value: 0.25, Unit: "ratio"},
		},
		Events: []collector.CollectorEvent{
			{Severity: collector.EventSeverityWarning, Type: "metric_fetch_failed", MetricKey: "jvm_heap_used_bytes", Message: "not found"},
		},
	}

	pollID, err := store.SaveCollectionResult(context.Background(), result)
	if err != nil {
		t.Fatalf("SaveCollectionResult() error = %v", err)
	}
	if pollID == 0 {
		t.Fatal("pollID = 0, want non-zero")
	}

	snapshot, err := store.LatestSnapshot(context.Background(), "app")
	if err != nil {
		t.Fatalf("LatestSnapshot() error = %v", err)
	}
	if snapshot.PollID != pollID {
		t.Fatalf("snapshot PollID = %d, want %d", snapshot.PollID, pollID)
	}
	if snapshot.Status != "ok" {
		t.Fatalf("snapshot Status = %q, want ok", snapshot.Status)
	}
	if snapshot.AppRunID == nil {
		t.Fatal("snapshot AppRunID = nil, want app run id")
	}
	if snapshot.Result.TargetName != result.TargetName {
		t.Fatalf("TargetName = %q, want %q", snapshot.Result.TargetName, result.TargetName)
	}
	if snapshot.Result.ProcessStartTime == nil || !snapshot.Result.ProcessStartTime.Equal(processStart) {
		t.Fatalf("ProcessStartTime = %v, want %v", snapshot.Result.ProcessStartTime, processStart)
	}
	assertStoredSample(t, snapshot.Result.Samples, "http_requests_total", collector.MetricKindCounter, 42, "requests")
	assertStoredSample(t, snapshot.Result.Samples, "process_cpu_usage", collector.MetricKindGauge, 0.25, "ratio")
	if len(snapshot.Result.Events) != 1 {
		t.Fatalf("events len = %d, want 1", len(snapshot.Result.Events))
	}
	if snapshot.Result.Events[0].Severity != collector.EventSeverityWarning || snapshot.Result.Events[0].MetricKey != "jvm_heap_used_bytes" {
		t.Fatalf("event = %#v, want warning for jvm_heap_used_bytes", snapshot.Result.Events[0])
	}
}

func TestSaveCollectionResultPersistsErrorStatusAndSummary(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	result := &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC),
		PollFinishedAt: time.Date(2026, 7, 7, 10, 1, 1, 0, time.UTC),
		Events: []collector.CollectorEvent{
			{Severity: collector.EventSeverityError, Type: "health_fetch_failed", Message: "unauthorized"},
		},
	}

	if _, err := store.SaveCollectionResult(context.Background(), result); err != nil {
		t.Fatalf("SaveCollectionResult() error = %v", err)
	}

	snapshot, err := store.LatestSnapshot(context.Background(), "app")
	if err != nil {
		t.Fatalf("LatestSnapshot() error = %v", err)
	}
	if snapshot.Status != "error" {
		t.Fatalf("Status = %q, want error", snapshot.Status)
	}
	if snapshot.ErrorSummary != "unauthorized" {
		t.Fatalf("ErrorSummary = %q, want unauthorized", snapshot.ErrorSummary)
	}
	if len(snapshot.Result.Events) != 1 || snapshot.Result.Events[0].Severity != collector.EventSeverityError {
		t.Fatalf("events = %#v, want one error event", snapshot.Result.Events)
	}
}

func TestSaveCollectionResultRollsBackOnSampleInsertFailure(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	result := &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC),
		PollFinishedAt: time.Date(2026, 7, 7, 10, 1, 1, 0, time.UTC),
		Samples: []collector.MetricSample{
			{Key: "bad_metric", Kind: collector.MetricKind("histogram"), Value: 1},
		},
	}

	_, err := store.SaveCollectionResult(context.Background(), result)
	if err == nil {
		t.Fatal("SaveCollectionResult() error = nil, want sample insert error")
	}

	assertTableCount(t, store, "targets", 0)
	assertTableCount(t, store, "polls", 0)
	assertTableCount(t, store, "metric_samples", 0)
}

func TestLatestSnapshotReturnsNoRowsForUnknownTarget(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	_, err := store.LatestSnapshot(context.Background(), "missing")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("LatestSnapshot() error = %v, want sql.ErrNoRows", err)
	}
}

func TestSeriesComputesCounterDeltasAndAverageLatency(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	runStart := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	appRunID, err := store.EnsureAppRun(ctx, "app", &runStart, runStart)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}

	saveSeriesPoll(t, store, appRunID, time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC), map[string]float64{
		"http_requests_total":             100,
		"http_404_total":                  1,
		"http_4xx_total":                  3,
		"http_5xx_total":                  0,
		"http_request_time_total_seconds": 20,
		"jvm_heap_used_bytes":             1024,
		"process_cpu_usage":               0.1,
	})
	saveSeriesPoll(t, store, appRunID, time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC), map[string]float64{
		"http_requests_total":             125,
		"http_404_total":                  2,
		"http_4xx_total":                  5,
		"http_5xx_total":                  1,
		"http_request_time_total_seconds": 25,
		"jvm_heap_used_bytes":             2048,
		"process_cpu_usage":               0.2,
	})

	series, err := store.Series(ctx, "app", time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC), time.Date(2026, 7, 7, 10, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 1 {
		t.Fatalf("series points len = %d, want 1", len(series.Points))
	}
	point := series.Points[0]
	assertFloatPointer(t, "Requests", point.Requests, 25)
	assertFloatPointer(t, "HTTP404", point.HTTP404, 1)
	assertFloatPointer(t, "HTTP4xx", point.HTTP4xx, 2)
	assertFloatPointer(t, "HTTP5xx", point.HTTP5xx, 1)
	assertFloatPointer(t, "AverageLatencySeconds", point.AverageLatencySeconds, 0.2)
	assertFloatPointer(t, "HeapUsedBytes", point.HeapUsedBytes, 2048)
	assertFloatPointer(t, "ProcessCPUUsage", point.ProcessCPUUsage, 0.2)
}

func TestPing(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestPingFailsWhenStoreIsNil(t *testing.T) {
	var nilStore *Store
	err := nilStore.Ping(context.Background())
	if err == nil {
		t.Fatal("Ping() error = nil, want error for nil store")
	}
}

func TestDeletePollsBeforeDeletesExpiredPollsAndCascades(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	oldStart := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	newStart := cutoff.Add(time.Minute)

	saveRetentionPoll(t, store, oldStart)
	saveRetentionPoll(t, store, cutoff)
	saveRetentionPoll(t, store, newStart)

	deleted, err := store.DeletePollsBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeletePollsBefore() error = %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}

	assertTableCount(t, store, "polls", 2)
	assertTableCount(t, store, "metric_samples", 2)
	assertTableCount(t, store, "collector_events", 2)

	series, err := store.Series(ctx, "app", oldStart.Add(-time.Hour), newStart.Add(time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 2 {
		t.Fatalf("series points len = %d, want 2", len(series.Points))
	}
	if !series.Points[0].Timestamp.Equal(cutoff) {
		t.Fatalf("first retained point timestamp = %v, want cutoff %v", series.Points[0].Timestamp, cutoff)
	}
}

func TestLatestSuccessfulSnapshot(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	processStart := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	result := &collector.CollectionResult{
		TargetName:       "app",
		PollStartedAt:    time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC),
		PollFinishedAt:   time.Date(2026, 7, 7, 10, 1, 1, 0, time.UTC),
		HealthStatus:     "UP",
		DBHealthStatus:   "UP",
		ProcessStartTime: &processStart,
		Samples: []collector.MetricSample{
			{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: 42, Unit: "requests"},
		},
	}

	pollID, err := store.SaveCollectionResult(context.Background(), result)
	if err != nil {
		t.Fatalf("SaveCollectionResult() error = %v", err)
	}

	snapshot, err := store.LatestSuccessfulSnapshot(context.Background(), "app")
	if err != nil {
		t.Fatalf("LatestSuccessfulSnapshot() error = %v", err)
	}
	if snapshot.PollID != pollID {
		t.Fatalf("PollID = %d, want %d", snapshot.PollID, pollID)
	}
	if snapshot.Status != "ok" {
		t.Fatalf("Status = %q, want ok", snapshot.Status)
	}
}

func TestLatestSuccessfulSnapshotSkipsErrorPolls(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	result := &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC),
		PollFinishedAt: time.Date(2026, 7, 7, 10, 1, 1, 0, time.UTC),
		Events:         []collector.CollectorEvent{{Severity: collector.EventSeverityError, Type: "health_fetch_failed", Message: "unauthorized"}},
	}

	if _, err := store.SaveCollectionResult(context.Background(), result); err != nil {
		t.Fatalf("SaveCollectionResult() error = %v", err)
	}

	_, err := store.LatestSuccessfulSnapshot(context.Background(), "app")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("LatestSuccessfulSnapshot() error = %v, want sql.ErrNoRows", err)
	}
}

func TestLatestSuccessfulSnapshotReturnsNoRowsForUnknownTarget(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	_, err := store.LatestSuccessfulSnapshot(context.Background(), "missing")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("LatestSuccessfulSnapshot() error = %v, want sql.ErrNoRows", err)
	}
}

func TestEvents(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	start := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	result := &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  start,
		PollFinishedAt: start.Add(time.Second),
		HealthStatus:   "DOWN",
		Events: []collector.CollectorEvent{
			{Severity: collector.EventSeverityError, Type: "health_fetch_failed", Message: "connection refused"},
			{Severity: collector.EventSeverityWarning, Type: "missing_metric", MetricKey: "process_cpu_usage", Message: "not found"},
		},
	}

	if _, err := store.SaveCollectionResult(context.Background(), result); err != nil {
		t.Fatalf("SaveCollectionResult() error = %v", err)
	}

	events, err := store.Events(ctx, "app", start.Add(-time.Hour), start.Add(time.Hour))
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events len = %d, want 2", len(events))
	}
	if events[0].Type != "missing_metric" {
		t.Fatalf("first event type = %q, want missing_metric (latest first)", events[0].Type)
	}
	if events[1].Type != "health_fetch_failed" {
		t.Fatalf("second event type = %q, want health_fetch_failed", events[1].Type)
	}
	if events[1].Severity != "error" {
		t.Fatalf("event severity = %q, want error", events[1].Severity)
	}
	if !events[1].Timestamp.Equal(start) {
		t.Fatalf("event timestamp = %v, want %v", events[1].Timestamp, start)
	}
}

func TestEventsReturnsEmptyForNoData(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	events, err := store.Events(context.Background(), "app", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if events == nil {
		t.Fatal("events = nil, want non-nil empty slice")
	}
	if len(events) != 0 {
		t.Fatalf("events len = %d, want 0", len(events))
	}
}

func TestEventsValidatesTargetName(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	_, err := store.Events(context.Background(), "", time.Now(), time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("Events() error = nil, want target name error")
	}
}

func TestEventsValidatesRange(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Now()
	_, err := store.Events(context.Background(), "app", now, now.Add(-time.Hour))
	if err == nil {
		t.Fatal("Events() error = nil, want start before end error")
	}
}

func TestSeriesOmitsCounterDeltasOnResetAndAppRunChange(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	firstRunStart := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	firstRunID, err := store.EnsureAppRun(ctx, "app", &firstRunStart, firstRunStart)
	if err != nil {
		t.Fatalf("EnsureAppRun(first) error = %v", err)
	}
	secondRunStart := time.Date(2026, 7, 7, 10, 3, 0, 0, time.UTC)
	secondRunID, err := store.EnsureAppRun(ctx, "app", &secondRunStart, secondRunStart)
	if err != nil {
		t.Fatalf("EnsureAppRun(second) error = %v", err)
	}

	saveSeriesPoll(t, store, firstRunID, time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC), map[string]float64{
		"http_requests_total":             100,
		"http_request_time_total_seconds": 20,
	})
	saveSeriesPoll(t, store, firstRunID, time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC), map[string]float64{
		"http_requests_total":             90,
		"http_request_time_total_seconds": 22,
	})
	saveSeriesPoll(t, store, secondRunID, time.Date(2026, 7, 7, 10, 2, 0, 0, time.UTC), map[string]float64{
		"http_requests_total":             5,
		"http_request_time_total_seconds": 1,
	})

	series, err := store.Series(ctx, "app", time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC), time.Date(2026, 7, 7, 10, 3, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 2 {
		t.Fatalf("series points len = %d, want 2", len(series.Points))
	}
	if series.Points[0].Requests != nil {
		t.Fatalf("reset requests delta = %v, want nil", *series.Points[0].Requests)
	}
	if series.Points[0].AverageLatencySeconds != nil {
		t.Fatalf("reset latency = %v, want nil", *series.Points[0].AverageLatencySeconds)
	}
	if series.Points[1].Requests != nil {
		t.Fatalf("new run requests delta = %v, want nil", *series.Points[1].Requests)
	}
	if series.Points[1].AverageLatencySeconds != nil {
		t.Fatalf("new run latency = %v, want nil", *series.Points[1].AverageLatencySeconds)
	}
}

func assertTableCount(t *testing.T, store *Store, table string, want int) {
	t.Helper()
	var got int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", table, got, want)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "statlite.sqlite"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return store
}

func assertStoredSample(t *testing.T, samples []collector.MetricSample, key string, kind collector.MetricKind, value float64, unit string) {
	t.Helper()
	for _, sample := range samples {
		if sample.Key != key {
			continue
		}
		if sample.Kind != kind || sample.Value != value || sample.Unit != unit {
			t.Fatalf("sample %s = %#v, want kind=%s value=%v unit=%s", key, sample, kind, value, unit)
		}
		return
	}
	t.Fatalf("missing sample %s in %#v", key, samples)
}

func saveSeriesPoll(t *testing.T, store *Store, appRunID int64, startedAt time.Time, values map[string]float64) {
	t.Helper()
	samples := make([]collector.MetricSample, 0, len(values))
	for key, value := range values {
		kind := collector.MetricKindCounter
		unit := "requests"
		switch key {
		case "http_request_time_total_seconds":
			unit = "seconds"
		case "jvm_heap_used_bytes":
			kind = collector.MetricKindGauge
			unit = "bytes"
		case "process_cpu_usage":
			kind = collector.MetricKindGauge
			unit = "ratio"
		}
		samples = append(samples, collector.MetricSample{Key: key, Kind: kind, Value: value, Unit: unit})
	}
	result := &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  startedAt,
		PollFinishedAt: startedAt.Add(time.Second),
		HealthStatus:   "UP",
		Samples:        samples,
	}
	if _, err := store.SaveCollectionResultWithAppRun(context.Background(), result, &appRunID); err != nil {
		t.Fatalf("SaveCollectionResultWithAppRun(%s) error = %v", startedAt, err)
	}
}

func saveRetentionPoll(t *testing.T, store *Store, startedAt time.Time) {
	t.Helper()
	result := &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  startedAt,
		PollFinishedAt: startedAt.Add(time.Second),
		HealthStatus:   "UP",
		Samples: []collector.MetricSample{
			{Key: "http_requests_total", Kind: collector.MetricKindCounter, Value: float64(startedAt.Unix()), Unit: "requests"},
		},
		Events: []collector.CollectorEvent{
			{Severity: collector.EventSeverityWarning, Type: "missing_metric", MetricKey: "process_cpu_usage", Message: "not found"},
		},
	}
	if _, err := store.SaveCollectionResult(context.Background(), result); err != nil {
		t.Fatalf("SaveCollectionResult(%s) error = %v", startedAt, err)
	}
}

func assertFloatPointer(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}
