package storage

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func f64(v float64) *float64 { return &v }

func TestAggregateSeriesLeavesSparseSeriesUnchanged(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	// One point every 5 minutes with 1-minute buckets → no shared buckets.
	series := &Series{
		Start: start,
		End:   end,
		Points: []SeriesPoint{
			{PollID: 1, Timestamp: start, Requests: f64(1)},
			{PollID: 2, Timestamp: start.Add(5 * time.Minute), Requests: f64(2)},
			{PollID: 3, Timestamp: start.Add(10 * time.Minute), Requests: f64(3)},
		},
	}

	out := AggregateSeries(series, time.Minute)
	if out != series {
		t.Fatal("AggregateSeries() should return the same series when at most one point per bucket")
	}
	if len(out.Points) != 3 {
		t.Fatalf("points = %d, want 3", len(out.Points))
	}
}

func TestAggregateSeriesAggregatesSharedBuckets(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Minute)
	appRunID1 := int64(10)
	appRunID2 := int64(11)
	series := &Series{
		Start: start,
		End:   end,
		Points: []SeriesPoint{
			{
				PollID:                1,
				Timestamp:             start,
				AppRunID:              &appRunID1,
				Requests:              f64(1),
				HTTP404:               f64(2),
				HTTP4xx:               f64(3),
				HTTP5xx:               f64(4),
				AverageLatencySeconds: f64(0.10),
				HeapUsedBytes:         f64(100),
				ProcessCPUUsage:       f64(0.20),
			},
			{
				PollID:                2,
				Timestamp:             start.Add(30 * time.Second),
				AppRunID:              &appRunID2,
				Requests:              f64(5),
				HTTP404:               f64(6),
				HTTP4xx:               f64(7),
				HTTP5xx:               f64(8),
				AverageLatencySeconds: f64(0.30),
				HeapUsedBytes:         f64(300),
				ProcessCPUUsage:       f64(0.40),
			},
			// Separate 1-minute bucket — remains its own point after aggregation.
			{
				PollID:    3,
				Timestamp: start.Add(time.Minute),
				AppRunID:  &appRunID1,
				Requests:  f64(9),
			},
		},
	}

	out := AggregateSeries(series, time.Minute)
	if out == series {
		t.Fatal("AggregateSeries() should return a new series when points share buckets")
	}
	if len(out.Points) != 2 {
		t.Fatalf("points = %d, want 2 (one shared bucket + one alone)", len(out.Points))
	}
	first := out.Points[0]
	if !first.Timestamp.Equal(start) {
		t.Fatalf("first bucket timestamp = %v, want %v", first.Timestamp, start)
	}
	assertFloatPtr(t, "requests", first.Requests, 6)
	assertFloatPtr(t, "http_404", first.HTTP404, 8)
	assertFloatPtr(t, "http_4xx", first.HTTP4xx, 10)
	assertFloatPtr(t, "http_5xx", first.HTTP5xx, 12)
	assertFloatPtr(t, "average_latency_seconds", first.AverageLatencySeconds, 0.20)
	assertFloatPtr(t, "heap_used_bytes", first.HeapUsedBytes, 200)
	assertFloatPtr(t, "process_cpu_usage", first.ProcessCPUUsage, 0.30)
	if first.PollID != 0 || first.AppRunID != nil {
		t.Fatalf("aggregated identity = poll %d/run %v, want omitted", first.PollID, first.AppRunID)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal aggregated point: %v", err)
	}
	if strings.Contains(string(encoded), "poll_id") || strings.Contains(string(encoded), "app_run_id") {
		t.Fatalf("aggregated JSON exposes identity: %s", encoded)
	}
	assertFloatPtr(t, "second bucket requests", out.Points[1].Requests, 9)
	if !out.Points[1].Timestamp.Equal(start.Add(time.Minute)) {
		t.Fatalf("second bucket timestamp = %v, want %v", out.Points[1].Timestamp, start.Add(time.Minute))
	}
	if out.Points[1].PollID != 3 || out.Points[1].AppRunID == nil || *out.Points[1].AppRunID != appRunID1 {
		t.Fatalf("singleton identity = poll %d/run %v, want poll 3/run %d", out.Points[1].PollID, out.Points[1].AppRunID, appRunID1)
	}
}

func TestAggregateSeriesNullsDoNotBecomeZeros(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	series := &Series{
		Start: start,
		End:   end,
		Points: []SeriesPoint{
			{PollID: 1, Timestamp: start, Requests: f64(1), HeapUsedBytes: nil, ProcessCPUUsage: nil},
			{PollID: 2, Timestamp: start.Add(30 * time.Second), Requests: nil, HeapUsedBytes: f64(100), HTTP404: nil},
		},
	}

	out := AggregateSeries(series, time.Minute)
	if len(out.Points) != 1 {
		t.Fatalf("points = %d, want 1", len(out.Points))
	}
	p := out.Points[0]
	assertFloatPtr(t, "requests", p.Requests, 1)
	assertFloatPtr(t, "heap_used_bytes", p.HeapUsedBytes, 100)
	if p.HTTP404 != nil {
		t.Fatalf("http_404 = %v, want nil", *p.HTTP404)
	}
	if p.HTTP4xx != nil {
		t.Fatalf("http_4xx = %v, want nil", *p.HTTP4xx)
	}
	if p.HTTP5xx != nil {
		t.Fatalf("http_5xx = %v, want nil", *p.HTTP5xx)
	}
	if p.AverageLatencySeconds != nil {
		t.Fatalf("average_latency_seconds = %v, want nil", *p.AverageLatencySeconds)
	}
	if p.ProcessCPUUsage != nil {
		t.Fatalf("process_cpu_usage = %v, want nil", *p.ProcessCPUUsage)
	}
}

func TestAggregateSeriesCoversFullTimeline(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Hour)
	const n = 601 // one point per minute
	points := make([]SeriesPoint, n)
	for i := 0; i < n; i++ {
		points[i] = SeriesPoint{
			PollID:    int64(i + 1),
			Timestamp: start.Add(time.Duration(i) * time.Minute),
			Requests:  f64(1),
		}
	}
	points[0].Requests = f64(100000)
	points[n-1].Requests = f64(200000)

	series := &Series{Start: start, End: end, Points: points}
	// 30-minute buckets (7d dashboard scale) across a dense timeline.
	out := AggregateSeries(series, 30*time.Minute)
	if len(out.Points) < 3 {
		t.Fatalf("points = %d, want several buckets", len(out.Points))
	}
	if len(out.Points) >= n {
		t.Fatalf("points = %d, want reduced below raw %d", len(out.Points), n)
	}
	if !out.Start.Equal(start) || !out.End.Equal(end) {
		t.Fatalf("start/end = %v/%v, want %v/%v", out.Start, out.End, start, end)
	}

	first, last := out.Points[0], out.Points[len(out.Points)-1]
	if !first.Timestamp.Equal(start) {
		t.Fatalf("first bucket = %v, want range start %v", first.Timestamp, start)
	}
	mid := start.Add(end.Sub(start) / 2)
	if !last.Timestamp.After(mid) {
		t.Fatalf("last bucket = %v, want after midpoint %v", last.Timestamp, mid)
	}
	if first.Requests == nil || *first.Requests < 100000 {
		t.Fatalf("first bucket requests = %v, want early marker", first.Requests)
	}
	if last.Requests == nil || *last.Requests < 200000 {
		t.Fatalf("last bucket requests = %v, want late marker", last.Requests)
	}
	for i := 1; i < len(out.Points); i++ {
		if !out.Points[i].Timestamp.After(out.Points[i-1].Timestamp) {
			t.Fatalf("not chronological at %d", i)
		}
	}
}

func TestAggregateSeriesFirstPartialBucketStartsAtRangeStart(t *testing.T) {
	start := time.Date(2026, 7, 1, 12, 17, 30, 0, time.UTC)
	series := &Series{
		Start: start,
		End:   start.Add(time.Hour),
		Points: []SeriesPoint{
			{PollID: 1, Timestamp: start.Add(10 * time.Second), Requests: f64(1)},
			{PollID: 2, Timestamp: start.Add(20 * time.Second), Requests: f64(2)},
			{PollID: 3, Timestamp: time.Date(2026, 7, 1, 12, 30, 10, 0, time.UTC), Requests: f64(3)},
			{PollID: 4, Timestamp: time.Date(2026, 7, 1, 12, 30, 20, 0, time.UTC), Requests: f64(4)},
		},
	}

	out := AggregateSeries(series, 30*time.Minute)
	if len(out.Points) != 2 {
		t.Fatalf("points = %d, want 2", len(out.Points))
	}
	if !out.Points[0].Timestamp.Equal(start) {
		t.Fatalf("first bucket timestamp = %v, want range start %v", out.Points[0].Timestamp, start)
	}
	wantSecond := time.Date(2026, 7, 1, 12, 30, 0, 0, time.UTC)
	if !out.Points[1].Timestamp.Equal(wantSecond) {
		t.Fatalf("second bucket timestamp = %v, want UTC boundary %v", out.Points[1].Timestamp, wantSecond)
	}
	for i, point := range out.Points {
		if point.Timestamp.Before(out.Start) {
			t.Fatalf("point %d timestamp %v precedes start %v", i, point.Timestamp, out.Start)
		}
	}
}

func TestAggregateSeriesAggregatesRestartAwareDeltas(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	series := &Series{
		Start: start,
		End:   end,
		Points: []SeriesPoint{
			{PollID: 1, Timestamp: start, Requests: f64(10)},
			{PollID: 2, Timestamp: start.Add(10 * time.Second), Requests: nil},
			{PollID: 3, Timestamp: start.Add(20 * time.Second), Requests: f64(4)},
			{PollID: 4, Timestamp: start.Add(30 * time.Second), Requests: f64(6)},
		},
	}

	out := AggregateSeries(series, time.Minute)
	if len(out.Points) != 1 {
		t.Fatalf("points = %d, want 1", len(out.Points))
	}
	assertFloatPtr(t, "requests", out.Points[0].Requests, 20)
}

func TestAggregateSeriesEmptyOrZeroDuration(t *testing.T) {
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	empty := &Series{Start: start, End: start.Add(time.Hour), Points: []SeriesPoint{}}
	if AggregateSeries(empty, time.Minute) != empty {
		t.Fatal("empty series should be unchanged")
	}
	series := &Series{
		Start:  start,
		End:    start.Add(time.Hour),
		Points: []SeriesPoint{{PollID: 1, Timestamp: start, Requests: f64(1)}},
	}
	if AggregateSeries(series, 0) != series {
		t.Fatal("zero bucket duration should leave series unchanged")
	}
}

func TestClockBucketStartIsStableAcrossRollingSeriesStart(t *testing.T) {
	// Same sample timestamps must land in the same bucket when only series.Start
	// shifts (as it does every refresh for rolling 1h/7d/30d ranges).
	ts := time.Date(2026, 7, 1, 12, 17, 42, 0, time.UTC)
	points := []SeriesPoint{
		{PollID: 1, Timestamp: ts, Requests: f64(1)},
		{PollID: 2, Timestamp: ts.Add(10 * time.Second), Requests: f64(2)},
	}
	bucket := 30 * time.Minute
	wantBucket := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

	for _, rangeStart := range []time.Time{
		ts.Add(-7 * 24 * time.Hour),
		ts.Add(-7*24*time.Hour + 30*time.Second),
		ts.Add(-time.Hour),
		ts.Add(-time.Hour + 17*time.Second),
	} {
		series := &Series{Start: rangeStart, End: rangeStart.Add(7 * 24 * time.Hour), Points: points}
		out := AggregateSeries(series, bucket)
		if len(out.Points) != 1 {
			t.Fatalf("rangeStart=%v: points = %d, want 1", rangeStart, len(out.Points))
		}
		if !out.Points[0].Timestamp.Equal(wantBucket) {
			t.Fatalf("rangeStart=%v: bucket = %v, want stable %v", rangeStart, out.Points[0].Timestamp, wantBucket)
		}
		assertFloatPtr(t, "requests", out.Points[0].Requests, 3)
	}
}

func TestClockBucketStartAlignsToDuration(t *testing.T) {
	ts := time.Date(2026, 7, 1, 12, 17, 42, 0, time.UTC)
	cases := []struct {
		dur  time.Duration
		want time.Time
	}{
		{time.Minute, time.Date(2026, 7, 1, 12, 17, 0, 0, time.UTC)},
		{5 * time.Minute, time.Date(2026, 7, 1, 12, 15, 0, 0, time.UTC)},
		{30 * time.Minute, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)},
		{2 * time.Hour, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		got := clockBucketStart(ts, tc.dur)
		if !got.Equal(tc.want) {
			t.Fatalf("clockBucketStart(%v) = %v, want %v", tc.dur, got, tc.want)
		}
	}
}

func assertFloatPtr(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if math.Abs(*got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}
