package storage

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

func TestPublicMetricsGroupsEveryCadenceAtMinuteStarts(t *testing.T) {
	for _, cadence := range []time.Duration{time.Second, 10 * time.Second, 30 * time.Second, time.Minute} {
		t.Run(cadence.String(), func(t *testing.T) {
			store := openTestStore(t)
			defer store.Close()
			ctx := context.Background()
			base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
			run, err := store.EnsureAppRun(ctx, "app", &base, base)
			if err != nil {
				t.Fatal(err)
			}
			for i, ts := 0, base; !ts.After(base.Add(time.Minute)); i, ts = i+1, ts.Add(cadence) {
				saveSeriesPoll(t, store, run, ts, map[string]float64{
					"http_requests_total": float64(i * 2), "http_4xx_total": float64(i),
					"http_5xx_total": float64(i), "http_request_time_total_seconds": float64(i),
				})
			}
			series, err := store.BoundedSeries(ctx, "app", base, base.Add(time.Minute), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			got := GroupPublicMetrics(series)
			wantSlots := 2
			if cadence == time.Minute {
				wantSlots = 1
			}
			if len(got.Points) != wantSlots {
				t.Fatalf("points = %d, want %d", len(got.Points), wantSlots)
			}
			if cadence < time.Minute {
				if !got.Points[0].Timestamp.Equal(base) || !got.Points[1].Timestamp.Equal(base.Add(time.Minute)) {
					t.Fatalf("minute slots = %v, %v", got.Points[0].Timestamp, got.Points[1].Timestamp)
				}
				assertFloatPointer(t, "first requests", got.Points[0].Requests, float64((time.Minute/cadence-1)*2))
				assertFloatPointer(t, "latency requests", got.Points[0].LatencyRequests, float64((time.Minute/cadence-1)*2))
				assertFloatPointer(t, "latency", got.Points[0].AverageLatencySeconds, 0.5)
			} else if !got.Points[0].Timestamp.Equal(base.Add(time.Minute)) {
				t.Fatalf("singleton slot = %v", got.Points[0].Timestamp)
			}
		})
	}
}

func TestPublicMetricsRequiresPairedStatusBaselinesAndKeepsHTTPFreshness(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	run, err := store.EnsureAppRun(ctx, "app", &base, base)
	if err != nil {
		t.Fatal(err)
	}
	saveSeriesPoll(t, store, run, base, map[string]float64{"http_requests_total": 10, "http_4xx_total": 0, "http_5xx_total": 0})
	saveSeriesPoll(t, store, run, base.Add(10*time.Second), map[string]float64{"http_requests_total": 20, "http_4xx_total": 1, "http_5xx_total": 2})
	saveSeriesPoll(t, store, run, base.Add(20*time.Second), map[string]float64{"http_4xx_total": 2, "http_5xx_total": 3})
	saveSeriesPoll(t, store, run, base.Add(30*time.Second), map[string]float64{"http_requests_total": 30})
	saveSeriesPoll(t, store, run, base.Add(40*time.Second), map[string]float64{"runtime_heap_used_bytes": 100})
	series, err := store.BoundedSeries(ctx, "app", base.Add(10*time.Second), base.Add(40*time.Second), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got := GroupPublicMetrics(series)
	if len(got.Points) != 1 {
		t.Fatalf("points = %d", len(got.Points))
	}
	p := got.Points[0]
	assertFloatPointer(t, "requests", p.Requests, 20)
	if p.HTTP4xx != nil || p.HTTP5xx != nil || p.HTTP5xxRate != nil {
		t.Fatalf("incomplete status counts = %#v", p)
	}
	if got.LatestHTTPObservationAt == nil || !got.LatestHTTPObservationAt.Equal(base.Add(30*time.Second)) {
		t.Fatalf("HTTP freshness = %v", got.LatestHTTPObservationAt)
	}
	assertFloatPointer(t, "memory", p.RuntimeMemoryBytes, 100)
}

func TestPublicMetricsRejectsDifferentStatusBaselinePolls(t *testing.T) {
	for _, tc := range []struct {
		name        string
		matchingKey string
		lateKey     string
	}{
		{name: "4xx differs", matchingKey: "http_5xx_total", lateKey: "http_4xx_total"},
		{name: "5xx differs", matchingKey: "http_4xx_total", lateKey: "http_5xx_total"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			defer store.Close()
			ctx := context.Background()
			base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
			run, err := store.EnsureAppRun(ctx, "app", &base, base)
			if err != nil {
				t.Fatal(err)
			}
			saveSeriesPoll(t, store, run, base, map[string]float64{
				"http_requests_total": 10, "http_4xx_total": 0, "http_5xx_total": 0,
			})
			saveSeriesPoll(t, store, run, base.Add(10*time.Second), map[string]float64{
				"http_requests_total": 20, tc.matchingKey: 1,
			})
			saveSeriesPoll(t, store, run, base.Add(20*time.Second), map[string]float64{tc.lateKey: 1})
			saveSeriesPoll(t, store, run, base.Add(30*time.Second), map[string]float64{
				"http_requests_total": 30, "http_4xx_total": 2, "http_5xx_total": 2,
			})
			saveSeriesPoll(t, store, run, base.Add(40*time.Second), map[string]float64{
				"http_requests_total": 40, "http_4xx_total": 3, "http_5xx_total": 3,
			})
			series, err := store.BoundedSeries(ctx, "app", base.Add(30*time.Second), base.Add(41*time.Second), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			if len(series.Points) != 2 || series.Points[0].HTTP4xx == nil || series.Points[0].HTTP5xx == nil {
				t.Fatalf("expected individually usable status deltas, got %#v", series.Points)
			}
			got := GroupPublicMetrics(series)
			if len(got.Points) != 1 {
				t.Fatalf("points = %#v", got.Points)
			}
			p := got.Points[0]
			assertFloatPointer(t, "requests", p.Requests, 20)
			if tc.lateKey == "http_4xx_total" {
				if p.HTTP4xx != nil {
					t.Fatalf("4xx with different baseline = %v", *p.HTTP4xx)
				}
				assertFloatPointer(t, "paired 5xx", p.HTTP5xx, 2)
				assertFloatPointer(t, "paired 5xx rate", p.HTTP5xxRate, .1)
			} else {
				assertFloatPointer(t, "paired 4xx", p.HTTP4xx, 2)
				if p.HTTP5xx != nil || p.HTTP5xxRate != nil {
					t.Fatalf("5xx with different baseline = %#v", p)
				}
			}
		})
	}
}

func TestBoundedSeriesRejectsPollOverflowBeforeSamples(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()
	stmt, err := store.db.PrepareContext(ctx, `INSERT INTO polls (target_id, started_at, finished_at, status) VALUES ((SELECT id FROM targets WHERE name = 'app'), ?, ?, 'ok')`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < PublicMetricsPollLimit; i++ {
		ts := formatSortableTime(base.Add(time.Duration(i) * time.Millisecond))
		if _, err := stmt.ExecContext(ctx, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.BoundedSeries(ctx, "app", base, base.Add(time.Minute), time.Time{}); err != nil {
		t.Fatalf("at poll limit: %v", err)
	}
	ts := formatSortableTime(base.Add(time.Duration(PublicMetricsPollLimit) * time.Millisecond))
	if _, err := stmt.ExecContext(ctx, ts, ts); err != nil {
		t.Fatal(err)
	}
	_, err = store.BoundedSeries(ctx, "app", base, base.Add(time.Minute), time.Time{})
	if err == nil || !strings.Contains(err.Error(), "poll limit exceeded") {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestPublicMetricsKeepsSparseGapDeltaInEndMinute(t *testing.T) {
	for _, gap := range []time.Duration{5 * time.Minute, time.Hour} {
		t.Run(gap.String(), func(t *testing.T) {
			store := openTestStore(t)
			defer store.Close()
			ctx := context.Background()
			base := time.Date(2026, 9, 24, 10, 0, 30, 0, time.UTC)
			run, err := store.EnsureAppRun(ctx, "app", &base, base)
			if err != nil {
				t.Fatal(err)
			}
			saveSeriesPoll(t, store, run, base, map[string]float64{"http_requests_total": 10, "http_4xx_total": 0, "http_5xx_total": 0})
			end := base.Add(gap)
			saveSeriesPoll(t, store, run, end, map[string]float64{"http_requests_total": 30, "http_4xx_total": 2, "http_5xx_total": 4})
			series, err := store.BoundedSeries(ctx, "app", base.Add(time.Second), end, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			got := GroupPublicMetrics(series)
			if len(got.Points) != 1 || !got.Points[0].Timestamp.Equal(end.Truncate(time.Minute)) {
				t.Fatalf("sparse slots = %#v", got.Points)
			}
			assertFloatPointer(t, "requests", got.Points[0].Requests, 20)
			assertFloatPointer(t, "4xx", got.Points[0].HTTP4xx, 2)
			assertFloatPointer(t, "5xx rate", got.Points[0].HTTP5xxRate, .2)
		})
	}
}

func TestPublicMetricsSeparatesStatusCoverageAndAveragesAvailableGauges(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	run, err := store.EnsureAppRun(ctx, "app", &base, base)
	if err != nil {
		t.Fatal(err)
	}
	saveSeriesPoll(t, store, run, base, map[string]float64{"http_requests_total": 10, "http_4xx_total": 0, "http_5xx_total": 0})
	saveSeriesPoll(t, store, run, base.Add(10*time.Second), map[string]float64{
		"http_requests_total": 20, "http_4xx_total": 1, "http_5xx_total": 2,
		"process_cpu_usage": .2, "runtime_heap_used_bytes": 100,
		"host_cpu_usage": .2, "host_memory_used_bytes": 20, "host_memory_total_bytes": 100,
		"host_disk_used_bytes": 80, "host_disk_total_bytes": 100,
	})
	saveSeriesPoll(t, store, run, base.Add(20*time.Second), map[string]float64{
		"http_requests_total": 30, "http_4xx_total": 2,
		"process_cpu_usage": .4, "runtime_heap_used_bytes": 300,
		"host_cpu_usage": .6, "host_memory_used_bytes": 90, "host_memory_total_bytes": 100,
		"host_disk_used_bytes": 120, "host_disk_total_bytes": 100,
	})
	series, err := store.BoundedSeries(ctx, "app", base.Add(10*time.Second), base.Add(20*time.Second), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got := GroupPublicMetrics(series)
	if len(got.Points) != 1 {
		t.Fatalf("points = %d", len(got.Points))
	}
	p := got.Points[0]
	assertFloatPointer(t, "requests", p.Requests, 20)
	assertFloatPointer(t, "4xx", p.HTTP4xx, 2)
	if p.HTTP5xx != nil || p.HTTP5xxRate != nil {
		t.Fatalf("incomplete 5xx = %#v", p)
	}
	assertNear(t, "cpu", p.ProcessCPUCores, .3)
	assertFloatPointer(t, "memory", p.RuntimeMemoryBytes, 200)
	assertNear(t, "host cpu", p.HostCPUUsage, .4)
	assertNear(t, "host memory", p.HostMemoryUsage, .55)
	assertNear(t, "host disk", p.HostDiskUsage, .8)
}

func assertNear(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil || math.Abs(*got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func TestPublicMetricsOmitsRestartAndResetDeltas(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	firstRun, err := store.EnsureAppRun(ctx, "app", &base, base)
	if err != nil {
		t.Fatal(err)
	}
	restartedAt := base.Add(10 * time.Second)
	secondRun, err := store.EnsureAppRun(ctx, "app", &restartedAt, restartedAt)
	if err != nil {
		t.Fatal(err)
	}
	saveSeriesPoll(t, store, firstRun, base, map[string]float64{"http_requests_total": 100})
	saveSeriesPoll(t, store, secondRun, restartedAt, map[string]float64{"http_requests_total": 2})
	saveSeriesPoll(t, store, secondRun, base.Add(20*time.Second), map[string]float64{"http_requests_total": 5})
	saveSeriesPoll(t, store, secondRun, base.Add(30*time.Second), map[string]float64{"http_requests_total": 1})
	series, err := store.BoundedSeries(ctx, "app", base, base.Add(30*time.Second), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got := GroupPublicMetrics(series)
	if len(got.Points) != 1 {
		t.Fatalf("points = %#v", got.Points)
	}
	assertFloatPointer(t, "requests", got.Points[0].Requests, 3)
	if got.LatestHTTPObservationAt == nil || !got.LatestHTTPObservationAt.Equal(base.Add(20*time.Second)) {
		t.Fatalf("HTTP freshness = %v", got.LatestHTTPObservationAt)
	}
}

func TestPublicMetricsKeepsSelfHostSamplesSeparateFromApp(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	run, err := store.EnsureAppRun(ctx, "app", &base, base)
	if err != nil {
		t.Fatal(err)
	}
	saveSeriesPoll(t, store, run, base, map[string]float64{"http_requests_total": 10})
	saveSeriesPoll(t, store, run, base.Add(10*time.Second), map[string]float64{"http_requests_total": 20})
	if err := store.RegisterTargets(ctx, []TargetIdentity{{Name: "statlite-self", Type: "statlite"}}); err != nil {
		t.Fatal(err)
	}
	self := &collector.CollectionResult{TargetName: "statlite-self", PollStartedAt: base.Add(10 * time.Second), PollFinishedAt: base.Add(11 * time.Second), Samples: []collector.MetricSample{
		{Key: "host_cpu_usage", Kind: collector.MetricKindGauge, Value: .7},
		{Key: "host_memory_used_bytes", Kind: collector.MetricKindGauge, Value: 70},
		{Key: "host_memory_total_bytes", Kind: collector.MetricKindGauge, Value: 100},
	}}
	if _, err := store.SaveCollectionResult(ctx, self); err != nil {
		t.Fatal(err)
	}
	appSeries, err := store.BoundedSeries(ctx, "app", base, base.Add(time.Minute), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	app := GroupPublicMetrics(appSeries)
	if len(app.Points) != 1 || app.Points[0].HostCPUUsage != nil || app.Points[0].HostMemoryUsage != nil {
		t.Fatalf("app points = %#v", app.Points)
	}
	selfSeries, err := store.BoundedSeries(ctx, "statlite-self", base, base.Add(time.Minute), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	selfMetrics := GroupPublicMetrics(selfSeries)
	if len(selfMetrics.Points) != 1 {
		t.Fatalf("self points = %#v", selfMetrics.Points)
	}
	assertNear(t, "self host cpu", selfMetrics.Points[0].HostCPUUsage, .7)
	assertNear(t, "self host memory", selfMetrics.Points[0].HostMemoryUsage, .7)
}

func TestPublicMetricsWeightsLatencyByCoveredRequests(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	ctx := context.Background()
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	run, err := store.EnsureAppRun(ctx, "app", &base, base)
	if err != nil {
		t.Fatal(err)
	}
	saveSeriesPoll(t, store, run, base, map[string]float64{"http_requests_total": 0, "http_request_time_total_seconds": 0})
	saveSeriesPoll(t, store, run, base.Add(10*time.Second), map[string]float64{"http_requests_total": 10, "http_request_time_total_seconds": 1})
	saveSeriesPoll(t, store, run, base.Add(20*time.Second), map[string]float64{"http_requests_total": 40, "http_request_time_total_seconds": 10})
	saveSeriesPoll(t, store, run, base.Add(30*time.Second), map[string]float64{"http_requests_total": 50})
	series, err := store.BoundedSeries(ctx, "app", base.Add(10*time.Second), base.Add(30*time.Second), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	got := GroupPublicMetrics(series)
	if len(got.Points) != 1 {
		t.Fatalf("points = %#v", got.Points)
	}
	assertFloatPointer(t, "requests", got.Points[0].Requests, 50)
	assertFloatPointer(t, "latency requests", got.Points[0].LatencyRequests, 40)
	assertFloatPointer(t, "average latency", got.Points[0].AverageLatencySeconds, .25)
}
