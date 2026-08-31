package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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

	var schemaVersion int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if schemaVersion != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", schemaVersion, currentSchemaVersion)
	}
}

func TestOpenAdoptsExistingUnversionedSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "statlite.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open unversioned database: %v", err)
	}
	schema, err := schemaFS.ReadFile("schema.sql")
	if err != nil {
		db.Close()
		t.Fatalf("read embedded schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, string(schema)); err != nil {
		db.Close()
		t.Fatalf("initialize unversioned schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO targets (name, created_at) VALUES ('existing', '2026-08-28T00:00:00Z')`); err != nil {
		db.Close()
		t.Fatalf("insert existing data: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close unversioned database: %v", err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	var schemaVersion int
	if err := store.db.QueryRow("PRAGMA user_version").Scan(&schemaVersion); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if schemaVersion != currentSchemaVersion {
		t.Fatalf("schema version = %d, want %d", schemaVersion, currentSchemaVersion)
	}
	var targetName string
	if err := store.db.QueryRow("SELECT name FROM targets").Scan(&targetName); err != nil {
		t.Fatalf("read existing target: %v", err)
	}
	if targetName != "existing" {
		t.Fatalf("existing target name = %q, want existing", targetName)
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

func TestEnsureAppRunPreservesLegacyProcessStartTimeIdentity(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	processStart := time.Date(2026, 8, 28, 18, 6, 20, 0, time.UTC)
	seenAt := processStart.Add(time.Minute)
	result, err := store.db.ExecContext(ctx, `
INSERT INTO targets (name, created_at) VALUES (?, ?)
`, "app", "2026-08-28T18:06:20Z")
	if err != nil {
		t.Fatalf("insert legacy target: %v", err)
	}
	targetID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read target id: %v", err)
	}
	result, err = store.db.ExecContext(ctx, `
INSERT INTO app_runs (target_id, process_start_time, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?)
`, targetID, "2026-08-28T18:06:20Z", "2026-08-28T18:07:20Z", "2026-08-28T18:07:20Z")
	if err != nil {
		t.Fatalf("insert legacy app run: %v", err)
	}
	wantID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read legacy app run id: %v", err)
	}

	gotID, err := store.EnsureAppRun(ctx, "app", &processStart, seenAt)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}
	if gotID != wantID {
		t.Fatalf("EnsureAppRun() id = %d, want existing id %d", gotID, wantID)
	}

	var count int
	var storedProcessStart, storedLastSeen string
	if err := store.db.QueryRow(`
SELECT COUNT(*), MIN(process_start_time), MIN(last_seen_at)
FROM app_runs
WHERE target_id = ?
`, targetID).Scan(&count, &storedProcessStart, &storedLastSeen); err != nil {
		t.Fatalf("query app runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("app run count = %d, want 1", count)
	}
	if storedProcessStart != "2026-08-28T18:06:20Z" {
		t.Fatalf("process_start_time = %q, want legacy identity representation", storedProcessStart)
	}
	if storedLastSeen != "2026-08-28T18:07:20" {
		t.Fatalf("last_seen_at = %q, want sortable implicit-UTC representation", storedLastSeen)
	}
}

func TestSaveSpringContractResultRowDeltas(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/actuator/health":
			_, _ = w.Write([]byte(`{"status":"UP"}`))
		case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "":
			_, _ = w.Write([]byte(`{"name":"http.server.requests","measurements":[{"statistic":"COUNT","value":10},{"statistic":"TOTAL_TIME","value":2}],"availableTags":[{"tag":"status","values":["404"]}]}`))
		case r.URL.Path == "/actuator/metrics/http.server.requests" && r.URL.Query().Get("tag") == "status:404":
			http.Error(w, "missing status", http.StatusNotFound)
		case r.URL.Path == "/actuator/metrics/jvm.memory.used" && r.URL.Query().Get("tag") == "area:heap":
			_, _ = w.Write([]byte(`{"name":"jvm.memory.used","measurements":[{"statistic":"VALUE","value":1024}]}`))
		case r.URL.Path == "/actuator/metrics/process.cpu.usage":
			_, _ = w.Write([]byte(`{"name":"process.cpu.usage","measurements":[{"statistic":"VALUE","value":0.12}]}`))
		case r.URL.Path == "/actuator/metrics/process.start.time":
			_, _ = w.Write([]byte(`{"name":"process.start.time","measurements":[{"statistic":"VALUE","value":1700000000}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := collector.NewActuatorClient(server.URL+"/actuator", time.Second, nil)
	if err != nil {
		t.Fatalf("NewActuatorClient() error = %v", err)
	}
	result, err := collector.NewSpringActuatorCollector("spring-contract", client, false).Collect(ctx)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(result.Samples) != 6 || len(result.Events) != 1 {
		t.Fatalf("Spring contract result has %d samples/%d events, want 6/1: %#v", len(result.Samples), len(result.Events), result)
	}
	if event := result.Events[0]; event.MetricKey != "" ||
		!strings.Contains(event.Message, "http_404_total, http_4xx_total") ||
		!strings.Contains(event.Message, "/actuator/metrics/http.server.requests?tag=status%3A404") ||
		!strings.Contains(event.Message, "HTTP 404: missing status") {
		t.Fatalf("Spring contract event = %#v, want complete shared-failure diagnostic", event)
	}
	if result.ProcessStartTime == nil {
		t.Fatal("Spring contract ProcessStartTime = nil, want populated")
	}
	if _, err := store.EnsureAppRun(ctx, result.TargetName, result.ProcessStartTime, result.PollStartedAt.Add(-time.Second)); err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}

	tables := []string{"targets", "app_runs", "polls", "metric_samples", "collector_events"}
	before := tableCounts(t, store, tables)
	if _, err := store.SaveCollectionResult(ctx, result); err != nil {
		t.Fatalf("SaveCollectionResult() error = %v", err)
	}
	after := tableCounts(t, store, tables)
	wantDeltas := map[string]int{
		"targets":          0,
		"app_runs":         0,
		"polls":            1,
		"metric_samples":   len(result.Samples),
		"collector_events": len(result.Events),
	}
	for _, table := range tables {
		if delta := after[table] - before[table]; delta != wantDeltas[table] {
			t.Fatalf("%s row delta = %d, want %d; before=%d after=%d", table, delta, wantDeltas[table], before[table], after[table])
		}
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
	if series.LatestPoint == nil || series.LatestPoint.PollID != point.PollID {
		t.Fatalf("latest point = %#v, want raw poll %d", series.LatestPoint, point.PollID)
	}
	assertFloatPointer(t, "Requests", point.Requests, 25)
	assertFloatPointer(t, "HTTP404", point.HTTP404, 1)
	assertFloatPointer(t, "HTTP4xx", point.HTTP4xx, 2)
	assertFloatPointer(t, "HTTP5xx", point.HTTP5xx, 1)
	assertFloatPointer(t, "AverageLatencySeconds", point.AverageLatencySeconds, 0.2)
	assertFloatPointer(t, "HeapUsedBytes", point.HeapUsedBytes, 2048)
	assertFloatPointer(t, "ProcessCPUUsage", point.ProcessCPUUsage, 0.2)
}

func TestSeriesUsesLatestPreRangeSampleForEachCounter(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	runStart := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	appRunID, err := store.EnsureAppRun(ctx, "app", &runStart, runStart)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}

	firstPoll := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	saveSeriesPoll(t, store, appRunID, firstPoll, map[string]float64{
		"http_requests_total":             100,
		"http_404_total":                  1,
		"http_4xx_total":                  3,
		"http_5xx_total":                  0,
		"http_request_time_total_seconds": 20,
	})
	saveSeriesPoll(t, store, appRunID, firstPoll.Add(time.Minute), map[string]float64{
		"process_cpu_usage": 0.1,
	})
	saveSeriesPoll(t, store, appRunID, firstPoll.Add(2*time.Minute), map[string]float64{
		"http_requests_total":             125,
		"http_404_total":                  2,
		"http_4xx_total":                  5,
		"http_5xx_total":                  1,
		"http_request_time_total_seconds": 25,
	})

	start := firstPoll.Add(2 * time.Minute)
	series, err := store.Series(ctx, "app", start, start.Add(time.Minute))
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
}

func TestSeriesResolvesRuntimeMemoryPerPoll(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	runStart := time.Date(2026, 7, 7, 9, 0, 0, 0, time.UTC)
	appRunID, err := store.EnsureAppRun(ctx, "app", &runStart, runStart)
	if err != nil {
		t.Fatalf("EnsureAppRun() error = %v", err)
	}

	firstPoll := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	saveSeriesPollSamples(t, store, appRunID, firstPoll, []collector.MetricSample{
		{Key: "jvm_heap_used_bytes", Kind: collector.MetricKindGauge, Value: 100, Unit: "bytes"},
		{Key: "process_cpu_usage", Kind: collector.MetricKindGauge, Value: 0.1, Unit: "ratio"},
	})
	saveSeriesPollSamples(t, store, appRunID, firstPoll.Add(time.Minute), []collector.MetricSample{
		{Key: "runtime_heap_used_bytes", Kind: collector.MetricKindGauge, Value: 200, Unit: "bytes"},
		{Key: "process_cpu_usage", Kind: collector.MetricKindGauge, Value: 0.1, Unit: "ratio"},
	})
	saveSeriesPollSamples(t, store, appRunID, firstPoll.Add(2*time.Minute), []collector.MetricSample{
		{Key: "runtime_heap_used_bytes", Kind: collector.MetricKindGauge, Value: 300, Unit: "bytes"},
		{Key: "jvm_heap_used_bytes", Kind: collector.MetricKindGauge, Value: 999, Unit: "bytes"},
		{Key: "process_cpu_usage", Kind: collector.MetricKindGauge, Value: 0.1, Unit: "ratio"},
	})
	saveSeriesPollSamples(t, store, appRunID, firstPoll.Add(3*time.Minute), []collector.MetricSample{
		{Key: "runtime_heap_used_bytes", Kind: collector.MetricKindCounter, Value: 350, Unit: "bytes"},
		{Key: "jvm_heap_used_bytes", Kind: collector.MetricKindGauge, Value: 400, Unit: "bytes"},
		{Key: "process_cpu_usage", Kind: collector.MetricKindGauge, Value: 0.1, Unit: "ratio"},
	})
	saveSeriesPollSamples(t, store, appRunID, firstPoll.Add(4*time.Minute), []collector.MetricSample{
		// Historical stored-only metrics must remain readable but ignored by
		// series projection.
		{Key: "jvm_heap_max_bytes", Kind: collector.MetricKindGauge, Value: 500, Unit: "bytes"},
		{Key: "http_request_time_max_seconds", Kind: collector.MetricKindGauge, Value: 0.9, Unit: "seconds"},
		{Key: "process_cpu_usage", Kind: collector.MetricKindGauge, Value: 0.1, Unit: "ratio"},
	})

	wantStoredMemoryRows := map[string]int{
		"runtime_heap_used_bytes":       3,
		"jvm_heap_used_bytes":           3,
		"jvm_heap_max_bytes":            1,
		"http_request_time_max_seconds": 1,
	}
	assertStoredMetricKeyCounts(t, store, wantStoredMemoryRows)

	series, err := store.Series(ctx, "app", firstPoll, firstPoll.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("Series() error = %v", err)
	}
	if len(series.Points) != 5 {
		t.Fatalf("series points len = %d, want 5", len(series.Points))
	}
	assertFloatPointer(t, "historical JVM memory", series.Points[0].HeapUsedBytes, 100)
	assertFloatPointer(t, "runtime memory", series.Points[1].HeapUsedBytes, 200)
	assertFloatPointer(t, "preferred runtime memory", series.Points[2].HeapUsedBytes, 300)
	assertFloatPointer(t, "wrong-kind runtime fallback", series.Points[3].HeapUsedBytes, 400)
	if series.Points[4].HeapUsedBytes != nil {
		t.Fatalf("JVM maximum fallback = %v, want nil", *series.Points[4].HeapUsedBytes)
	}

	// Series reads raw rows only; querying mixed history must not rewrite or
	// migrate either the neutral or historical samples.
	assertStoredMetricKeyCounts(t, store, wantStoredMemoryRows)
}

func TestPing(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestAvailableTracksOpenStoreWithoutSQLiteQuery(t *testing.T) {
	store := openTestStore(t)
	if !store.Available() {
		t.Fatal("Available() = false, want true for an open store")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if store.Available() {
		t.Fatal("Available() = true, want false after Close")
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

	events, err := store.Events(ctx, "app", start.Add(-time.Hour), start.Add(time.Hour), 0)
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

	events, err := store.Events(context.Background(), "app", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), 0)
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

	_, err := store.Events(context.Background(), "", time.Now(), time.Now().Add(time.Hour), 0)
	if err == nil {
		t.Fatal("Events() error = nil, want target name error")
	}
}

func TestEventsValidatesRange(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	now := time.Now()
	_, err := store.Events(context.Background(), "app", now, now.Add(-time.Hour), 0)
	if err == nil {
		t.Fatal("Events() error = nil, want start before end error")
	}
}

func TestEventsRespectsLimitNewestFirst(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	const eventLimit = 20
	const total = 25
	for i := 0; i < total; i++ {
		started := base.Add(time.Duration(i) * time.Minute)
		result := &collector.CollectionResult{
			TargetName:     "app",
			PollStartedAt:  started,
			PollFinishedAt: started.Add(time.Second),
			HealthStatus:   "UP",
			Events: []collector.CollectorEvent{
				{Severity: collector.EventSeverityWarning, Type: "metric_fetch_failed", Message: fmt.Sprintf("event-%02d", i)},
			},
		}
		if _, err := store.SaveCollectionResult(ctx, result); err != nil {
			t.Fatalf("SaveCollectionResult(%d) error = %v", i, err)
		}
	}

	events, err := store.Events(ctx, "app", base.Add(-time.Hour), base.Add(time.Hour), eventLimit)
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if len(events) != eventLimit {
		t.Fatalf("events len = %d, want %d", len(events), eventLimit)
	}
	// Newest first: last inserted is event-24.
	if events[0].Message != "event-24" {
		t.Fatalf("first event message = %q, want event-24", events[0].Message)
	}
	if events[len(events)-1].Message != "event-05" {
		t.Fatalf("last event message = %q, want event-05", events[len(events)-1].Message)
	}
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.After(events[i-1].Timestamp) {
			t.Fatalf("events not newest-first at %d: %v then %v", i, events[i-1].Timestamp, events[i].Timestamp)
		}
	}
}

func TestEventsQueryAppliesSQLLimit(t *testing.T) {
	query, args := eventsQuery("app", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "", 20)
	if !strings.Contains(query, "LIMIT ?") {
		t.Fatalf("events query missing SQL LIMIT:\n%s", query)
	}
	if len(args) != 4 {
		t.Fatalf("args len = %d, want 4 (target, start, end, limit)", len(args))
	}
	if args[3] != 20 {
		t.Fatalf("limit arg = %v, want 20", args[3])
	}

	unlimited, unlimitedArgs := eventsQuery("app", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "", 0)
	if strings.Contains(unlimited, "LIMIT") {
		t.Fatalf("unlimited events query should omit LIMIT:\n%s", unlimited)
	}
	if len(unlimitedArgs) != 3 {
		t.Fatalf("unlimited args len = %d, want 3", len(unlimitedArgs))
	}

	const restartType = "restart_detected"
	typed, typedArgs := eventsQuery("app", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), restartType, 1)
	if !strings.Contains(typed, "e.event_type = ?") {
		t.Fatalf("typed events query missing event_type filter:\n%s", typed)
	}
	if len(typedArgs) != 5 {
		t.Fatalf("typed args len = %d, want 5", len(typedArgs))
	}
	if typedArgs[3] != restartType {
		t.Fatalf("event type arg = %v, want %s", typedArgs[3], restartType)
	}
}

func TestLatestEventByTypeFindsRestartOutsideRecentWindow(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()

	ctx := context.Background()
	base := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	const eventLimit = 20
	const restartType = "restart_detected"
	// One restart, then many newer noise events so a plain LIMIT 20 would hide it.
	if _, err := store.SaveCollectionResult(ctx, &collector.CollectionResult{
		TargetName:     "app",
		PollStartedAt:  base,
		PollFinishedAt: base.Add(time.Second),
		HealthStatus:   "UP",
		Events: []collector.CollectorEvent{
			{Severity: collector.EventSeverityWarning, Type: restartType, Message: "process start changed"},
		},
	}); err != nil {
		t.Fatalf("save restart: %v", err)
	}
	for i := 1; i <= 25; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		if _, err := store.SaveCollectionResult(ctx, &collector.CollectionResult{
			TargetName:     "app",
			PollStartedAt:  at,
			PollFinishedAt: at.Add(time.Second),
			HealthStatus:   "UP",
			Events: []collector.CollectorEvent{
				{Severity: collector.EventSeverityWarning, Type: "metric_fetch_failed", Message: fmt.Sprintf("noise-%02d", i)},
			},
		}); err != nil {
			t.Fatalf("save noise %d: %v", i, err)
		}
	}

	rangeStart, rangeEnd := base.Add(-time.Hour), base.Add(time.Hour)
	recent, err := store.Events(ctx, "app", rangeStart, rangeEnd, eventLimit)
	if err != nil {
		t.Fatalf("Events() error = %v", err)
	}
	if len(recent) != eventLimit {
		t.Fatalf("recent len = %d, want %d", len(recent), eventLimit)
	}
	for _, event := range recent {
		if event.Type == restartType {
			t.Fatal("limited Events unexpectedly included early restart_detected")
		}
	}

	restart, err := store.LatestEventByType(ctx, "app", restartType, rangeStart, rangeEnd)
	if err != nil {
		t.Fatalf("LatestEventByType() error = %v", err)
	}
	if restart == nil {
		t.Fatal("LatestEventByType() = nil, want restart")
	}
	if restart.Message != "process start changed" {
		t.Fatalf("restart message = %q", restart.Message)
	}
	if !restart.Timestamp.Equal(base) {
		t.Fatalf("restart timestamp = %v, want %v", restart.Timestamp, base)
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
		"http_404_total":                  8,
		"http_4xx_total":                  12,
		"http_5xx_total":                  4,
		"http_request_time_total_seconds": 20,
	})
	saveSeriesPoll(t, store, firstRunID, time.Date(2026, 7, 7, 10, 1, 0, 0, time.UTC), map[string]float64{
		"http_requests_total":             90,
		"http_404_total":                  1,
		"http_4xx_total":                  2,
		"http_5xx_total":                  1,
		"http_request_time_total_seconds": 22,
	})
	saveSeriesPoll(t, store, secondRunID, time.Date(2026, 7, 7, 10, 2, 0, 0, time.UTC), map[string]float64{
		"http_requests_total":             5,
		"http_404_total":                  0,
		"http_4xx_total":                  0,
		"http_5xx_total":                  0,
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
	if series.Points[0].HTTP404 != nil || series.Points[0].HTTP4xx != nil || series.Points[0].HTTP5xx != nil {
		t.Fatalf("reset status deltas = (%v, %v, %v), want nil", series.Points[0].HTTP404, series.Points[0].HTTP4xx, series.Points[0].HTTP5xx)
	}
	if series.Points[1].Requests != nil {
		t.Fatalf("new run requests delta = %v, want nil", *series.Points[1].Requests)
	}
	if series.Points[1].AverageLatencySeconds != nil {
		t.Fatalf("new run latency = %v, want nil", *series.Points[1].AverageLatencySeconds)
	}
	if series.Points[1].HTTP404 != nil || series.Points[1].HTTP4xx != nil || series.Points[1].HTTP5xx != nil {
		t.Fatalf("new run status deltas = (%v, %v, %v), want nil", series.Points[1].HTTP404, series.Points[1].HTTP4xx, series.Points[1].HTTP5xx)
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

func tableCounts(t *testing.T, store *Store, tables []string) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(tables))
	for _, table := range tables {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
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
		case "runtime_heap_used_bytes", "jvm_heap_used_bytes", "host_memory_used_bytes", "host_memory_total_bytes", "host_disk_used_bytes", "host_disk_total_bytes":
			kind = collector.MetricKindGauge
			unit = "bytes"
		case "process_cpu_usage":
			kind = collector.MetricKindGauge
			unit = "cores"
		case "host_cpu_usage":
			kind = collector.MetricKindGauge
			unit = "ratio"
		}
		samples = append(samples, collector.MetricSample{Key: key, Kind: kind, Value: value, Unit: unit})
	}
	saveSeriesPollSamples(t, store, appRunID, startedAt, samples)
}

func saveSeriesPollSamples(t *testing.T, store *Store, appRunID int64, startedAt time.Time, samples []collector.MetricSample) {
	t.Helper()
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

func assertStoredMetricKeyCounts(t *testing.T, store *Store, want map[string]int) {
	t.Helper()
	for key, wantCount := range want {
		var got int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM metric_samples WHERE metric_key = ?", key).Scan(&got); err != nil {
			t.Fatalf("count metric key %s: %v", key, err)
		}
		if got != wantCount {
			t.Fatalf("metric key %s count = %d, want %d", key, got, wantCount)
		}
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
