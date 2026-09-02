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
	"github.com/pvrlabs/statlite/internal/prometheus"
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

func TestNoPollLoadsStoredStateAndPreventsCollection(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	result := successfulResult(time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC), 12, 3)
	pollID, err := store.SaveCollectionResult(context.Background(), result)
	if err != nil {
		t.Fatalf("SaveCollectionResult() error = %v", err)
	}
	collector := newNotifyingSequenceCollector(nil)
	mon := newTestMonitor(t, store, collector)

	if err := mon.EnableNoPoll(context.Background()); err != nil {
		t.Fatalf("EnableNoPoll() error = %v", err)
	}
	if _, err := mon.PollNow(context.Background()); !errors.Is(err, ErrPollingDisabled) {
		t.Fatalf("PollNow() error = %v, want ErrPollingDisabled", err)
	}

	select {
	case <-collector.calls:
		t.Fatal("collector was called in no-poll mode")
	case <-time.After(20 * time.Millisecond):
	}
	latest := mon.LatestSnapshot()
	if latest == nil || latest.PollID != pollID || latest.Result.HealthStatus != "UP" {
		t.Fatalf("LatestSnapshot() = %#v, want stored poll %d", latest, pollID)
	}
	status := mon.Status()
	if status.LastStoredPollID != pollID || status.LastSuccessfulStoredPollID != pollID || status.LastSuccessfulPollAt == nil {
		t.Fatalf("Status() = %#v, want stored successful poll %d", status, pollID)
	}
}

func TestNoPollRestoresLatestSuccessAndTrailingFailures(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	started := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	successID, err := store.SaveCollectionResult(context.Background(), uptimeResult(started, 10))
	if err != nil {
		t.Fatalf("save success: %v", err)
	}
	var latestID int64
	for i := 1; i <= 2; i++ {
		at := started.Add(time.Duration(i) * time.Minute)
		latestID, err = store.SaveCollectionResult(context.Background(), &collector.CollectionResult{
			TargetName:     "app",
			PollStartedAt:  at,
			PollFinishedAt: at.Add(time.Second),
			Events: []collector.CollectorEvent{{
				Severity: collector.EventSeverityError,
				Type:     "target_unreachable",
				Message:  "connection refused",
			}},
		})
		if err != nil {
			t.Fatalf("save failure %d: %v", i, err)
		}
	}

	mon := newTestMonitor(t, store, newNotifyingSequenceCollector(nil))
	if err := mon.EnableNoPoll(context.Background()); err != nil {
		t.Fatalf("EnableNoPoll() error = %v", err)
	}
	status := mon.Status()
	if status.LastStoredPollID != latestID || status.ConsecutivePollFailures != 2 {
		t.Fatalf("Status() = %#v, want latest poll %d and 2 failures", status, latestID)
	}
	if status.LastSuccessfulStoredPollID != successID || status.LastSuccessfulPollAt == nil {
		t.Fatalf("Status() = %#v, want stored success %d", status, successID)
	}
}

func TestNoPollRestoresLastFailureAfterRecovery(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	started := time.Date(2026, 6, 2, 9, 0, 0, 0, time.UTC)
	if _, err := store.SaveCollectionResult(context.Background(), uptimeResult(started, 10)); err != nil {
		t.Fatalf("save initial success: %v", err)
	}
	failedAt := started.Add(time.Minute)
	if _, err := store.SaveCollectionResult(context.Background(), &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  failedAt,
		PollFinishedAt: failedAt.Add(time.Second),
		Events: []collector.CollectorEvent{{
			Severity: collector.EventSeverityError,
			Type:     "target_unreachable",
			Message:  "connection refused",
		}},
	}); err != nil {
		t.Fatalf("save failure: %v", err)
	}
	recoveredAt := started.Add(2 * time.Minute)
	recoveryID, err := store.SaveCollectionResult(context.Background(), uptimeResult(recoveredAt, 20))
	if err != nil {
		t.Fatalf("save recovery: %v", err)
	}

	mon := newTestMonitor(t, store, newNotifyingSequenceCollector(nil))
	if err := mon.EnableNoPoll(context.Background()); err != nil {
		t.Fatalf("EnableNoPoll() error = %v", err)
	}
	status := mon.Status()
	if status.ConsecutivePollFailures != 0 {
		t.Fatalf("ConsecutivePollFailures = %d, want 0", status.ConsecutivePollFailures)
	}
	if status.LastSuccessfulStoredPollID != recoveryID || status.LastSuccessfulPollAt == nil {
		t.Fatalf("Status() = %#v, want recovery poll %d", status, recoveryID)
	}
	wantFailedAt := failedAt.Add(time.Second)
	if status.LastFailedPollAt == nil || !status.LastFailedPollAt.Equal(wantFailedAt) {
		t.Fatalf("LastFailedPollAt = %v, want %v", status.LastFailedPollAt, wantFailedAt)
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

func TestPollNowDetectsQuarkusRestartWithoutNegativeCounterDelta(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	var scrape int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"UP","checks":[{"name":"Database connections health check","status":"UP"}]}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		scrape++
		if scrape == 1 {
			_, _ = w.Write([]byte("process_start_time_seconds 1770000000\nprocess_cpu_usage 0.2\nhttp_server_requests_seconds_count{method=\"GET\",outcome=\"SUCCESS\",status=\"200\"} 100\n"))
			return
		}
		_, _ = w.Write([]byte("process_start_time_seconds 1770000060\nprocess_cpu_usage 0.1\nhttp_server_requests_seconds_count{method=\"GET\",outcome=\"SUCCESS\",status=\"200\"} 2\n"))
	}))
	defer server.Close()
	client, err := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	healthClient, err := collector.NewQuarkusHealthClient(server.URL+"/health", time.Second, nil)
	if err != nil {
		t.Fatalf("NewQuarkusHealthClient() error = %v", err)
	}
	mon := newTestMonitor(t, store, collector.NewQuarkusCollector("app", server.URL, client, healthClient))

	first, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("first PollNow() error = %v", err)
	}
	second, err := mon.PollNow(context.Background())
	if err != nil {
		t.Fatalf("second PollNow() error = %v", err)
	}
	if first.AppRunID == nil || second.AppRunID == nil || *first.AppRunID == *second.AppRunID {
		t.Fatalf("app run ids = %v/%v, want distinct Quarkus runs", first.AppRunID, second.AppRunID)
	}
	if !hasEvent(second.Result.Events, EventTypeRestartDetected) {
		t.Fatalf("events = %#v, want restart detection", second.Result.Events)
	}
	series, err := store.Series(context.Background(), "app", first.Result.PollStartedAt.Add(-time.Second), second.Result.PollFinishedAt.Add(time.Second))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	for _, point := range series.Points {
		if point.Requests != nil && *point.Requests < 0 {
			t.Fatalf("requests delta = %v after Quarkus restart, want nonnegative", *point.Requests)
		}
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

func TestPollNowPreservesIncreasingCountersAcrossMonitorRecreation(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	started := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	first, err := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{{result: successfulResult(started, 100, 10)}}}).PollNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{{result: successfulResult(started, 150, 15)}}}).PollNow(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.AppRunID == nil || second.AppRunID == nil || *first.AppRunID != *second.AppRunID {
		t.Fatalf("app run ids = %v, %v; want continuity across collector recreation", first.AppRunID, second.AppRunID)
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

func TestStartAddsOneFollowUpPollForTargetWithoutHistory(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	processStart := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	targetCollector := newNotifyingSequenceCollector([]collectResult{
		{result: successfulResult(processStart, 100, 10)},
		{result: successfulResult(processStart, 105, 11)},
	})
	mon := newTestMonitor(t, store, targetCollector)
	mon.startupFollowUpDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon.Start(ctx)
	waitForCollection(t, targetCollector.calls)
	waitForCollection(t, targetCollector.calls)
	waitForStoredPolls(t, mon, 2)

	series, err := mon.Series(context.Background(), processStart, processStart.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 2 {
		t.Fatalf("series points = %d, want baseline and follow-up", len(series.Points))
	}
	assertMonitorFloatPointer(t, "follow-up requests", series.Points[1].Requests, 5)

	select {
	case <-targetCollector.calls:
		t.Fatal("Start() collected more than one startup follow-up")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestStartDoesNotAddFollowUpPollForTargetWithHistory(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	processStart := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	seed := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{
		{result: successfulResult(processStart, 100, 10)},
	}})
	if _, err := seed.PollNow(context.Background()); err != nil {
		t.Fatalf("seed PollNow() error = %v", err)
	}

	targetCollector := newNotifyingSequenceCollector([]collectResult{
		{result: successfulResult(processStart, 105, 11)},
	})
	mon := newTestMonitor(t, store, targetCollector)
	mon.startupFollowUpDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon.Start(ctx)
	waitForCollection(t, targetCollector.calls)

	select {
	case <-targetCollector.calls:
		t.Fatal("Start() added a startup follow-up despite stored history")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestStartAddsFollowUpWhenFirstPollStartsNewAppRun(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	previousStart := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	seed := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{
		{result: successfulResult(previousStart, 100, 10)},
	}})
	previous, err := seed.PollNow(context.Background())
	if err != nil {
		t.Fatalf("seed PollNow() error = %v", err)
	}

	currentStart := previousStart.Add(time.Hour)
	targetCollector := newNotifyingSequenceCollector([]collectResult{
		{result: successfulResult(currentStart, 5, 0.5)},
		{result: successfulResult(currentStart, 8, 0.8)},
	})
	mon := newTestMonitor(t, store, targetCollector)
	mon.startupFollowUpDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon.Start(ctx)
	waitForCollection(t, targetCollector.calls)
	waitForCollection(t, targetCollector.calls)
	waitForStoredPolls(t, mon, 3)

	current := mon.LatestSnapshot()
	if previous.AppRunID == nil || current == nil || current.AppRunID == nil || *previous.AppRunID == *current.AppRunID {
		t.Fatalf("app run ids = %v -> %v, want a new run", previous.AppRunID, current)
	}
	series, err := mon.Series(context.Background(), previousStart, currentStart.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 3 {
		t.Fatalf("series points = %d, want persisted baseline, restart baseline, and follow-up", len(series.Points))
	}
	if series.Points[1].Requests != nil {
		t.Fatalf("restart baseline requests = %v, want nil", *series.Points[1].Requests)
	}
	assertMonitorFloatPointer(t, "new-run follow-up requests", series.Points[2].Requests, 3)
}

func TestStartAddsFollowUpWhenSameRunHistoryLacksCurrentCounter(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	processStart := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	previousResult := successfulResult(processStart, 0, 0)
	previousResult.Samples = []collector.MetricSample{
		{Key: "process_cpu_usage", Kind: collector.MetricKindGauge, Value: 0.1, Unit: "ratio"},
	}
	seed := newTestMonitor(t, store, &sequenceCollector{results: []collectResult{{result: previousResult}}})
	previous, err := seed.PollNow(context.Background())
	if err != nil {
		t.Fatalf("seed PollNow() error = %v", err)
	}

	targetCollector := newNotifyingSequenceCollector([]collectResult{
		{result: successfulResult(processStart, 5, 0.5)},
		{result: successfulResult(processStart, 8, 0.8)},
	})
	mon := newTestMonitor(t, store, targetCollector)
	mon.startupFollowUpDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon.Start(ctx)
	waitForCollection(t, targetCollector.calls)
	waitForCollection(t, targetCollector.calls)
	waitForStoredPolls(t, mon, 3)

	current := mon.LatestSnapshot()
	if previous.AppRunID == nil || current == nil || current.AppRunID == nil || *previous.AppRunID != *current.AppRunID {
		t.Fatalf("app run ids = %v -> %v, want the same run", previous.AppRunID, current)
	}
	series, err := mon.Series(context.Background(), processStart, processStart.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 3 {
		t.Fatalf("series points = %d, want gauge history, counter baseline, and follow-up", len(series.Points))
	}
	if series.Points[1].Requests != nil {
		t.Fatalf("first counter sample requests = %v, want nil", *series.Points[1].Requests)
	}
	assertMonitorFloatPointer(t, "same-run follow-up requests", series.Points[2].Requests, 3)
}

func TestStartDoesNotAddFollowUpPollAfterFailedFirstPoll(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	processStart := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	failed := successfulResult(processStart, 100, 10)
	failed.Events = []collector.CollectorEvent{{
		Severity: collector.EventSeverityError,
		Type:     "required_metric_failed",
		Message:  "required metric unavailable",
	}}
	targetCollector := newNotifyingSequenceCollector([]collectResult{{result: failed}})
	mon := newTestMonitor(t, store, targetCollector)
	mon.startupFollowUpDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon.Start(ctx)
	waitForCollection(t, targetCollector.calls)
	waitForPollFailures(t, mon, 1)

	select {
	case <-targetCollector.calls:
		t.Fatal("Start() added a startup follow-up after a failed poll")
	case <-time.After(25 * time.Millisecond):
	}
}

func TestStartupFollowUpDoesNotExceedConfiguredInterval(t *testing.T) {
	if got := startupFollowUpWait(time.Second, defaultStartupFollowUpDelay); got != time.Second {
		t.Fatalf("startupFollowUpWait() = %v, want 1s configured interval", got)
	}
	if got := startupFollowUpWait(time.Minute, defaultStartupFollowUpDelay); got != defaultStartupFollowUpDelay {
		t.Fatalf("startupFollowUpWait() = %v, want %v preferred delay", got, defaultStartupFollowUpDelay)
	}
}

func TestStartDoesNotAddFollowUpPollForGaugeOnlyTarget(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	pollAt := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	targetCollector := newNotifyingSequenceCollector([]collectResult{{result: uptimeResult(pollAt, 60)}})
	mon := newTestMonitor(t, store, targetCollector)
	mon.startupFollowUpDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mon.Start(ctx)
	waitForCollection(t, targetCollector.calls)
	waitForStoredPolls(t, mon, 1)

	select {
	case <-targetCollector.calls:
		t.Fatal("Start() added a startup follow-up for a gauge-only target")
	case <-time.After(25 * time.Millisecond):
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

type notifyingSequenceCollector struct {
	mu      sync.Mutex
	results []collectResult
	index   int
	calls   chan struct{}
}

func newNotifyingSequenceCollector(results []collectResult) *notifyingSequenceCollector {
	return &notifyingSequenceCollector{results: results, calls: make(chan struct{}, len(results)+1)}
}

func (c *notifyingSequenceCollector) Collect(context.Context) (*collector.CollectionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls <- struct{}{}
	if c.index >= len(c.results) {
		return nil, errors.New("unexpected collection")
	}
	result := c.results[c.index]
	c.index++
	return result.result, result.err
}

func waitForCollection(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for collection")
	}
}

func waitForStoredPolls(t *testing.T, mon *Monitor, count int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if mon.Status().LastSuccessfulStoredPollID >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d stored polls", count)
}

func waitForPollFailures(t *testing.T, mon *Monitor, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if mon.Status().ConsecutivePollFailures >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d poll failures", count)
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
