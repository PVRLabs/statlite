package storage

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func dashboardTestPoll(t *testing.T, s *Store, id int64, at time.Time, run *int64, samples map[string]float64) {
	t.Helper()
	stamp := formatSortableTime(at)
	if _, err := s.db.Exec(`INSERT INTO polls(id,target_id,app_run_id,started_at,finished_at,status) VALUES(?,1,?,?,?,'ok')`, id, nullableInt64(run), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	for key, value := range samples {
		kind := "gauge"
		if strings.HasPrefix(key, "http_") {
			kind = "counter"
		}
		if _, err := s.db.Exec(`INSERT INTO metric_samples(poll_id,metric_key,metric_kind,value) VALUES(?,?,?,?)`, id, key, kind, value); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDashboardPollIDsEdges(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	run1, err := s.EnsureAppRun(t.Context(), "app", &base, base)
	if err != nil {
		t.Fatal(err)
	}
	other := base.Add(time.Minute)
	run2, err := s.EnsureAppRun(t.Context(), "app", &other, other)
	if err != nil {
		t.Fatal(err)
	}
	for _, poll := range []struct {
		id     int64
		offset time.Duration
		run    *int64
	}{
		{90, 0, &run1}, {20, time.Minute, &run1}, {30, 2 * time.Minute, nil},
		{40, 3 * time.Minute, &run1}, {50, 3 * time.Minute, &run1}, {31, 4 * time.Minute, nil},
		{60, 30*time.Minute - time.Second, &run2}, {70, 30 * time.Minute, &run2}, {80, time.Hour, nil},
	} {
		dashboardTestPoll(t, s, poll.id, base.Add(poll.offset), poll.run, nil)
	}
	for _, tc := range []struct {
		name        string
		offset, end time.Duration
		want        []int64
	}{
		{"full", 0, time.Hour, []int64{30, 31, 50, 60, 70, 80, 90}},
		{"partial", time.Minute, time.Hour, []int64{20, 30, 31, 50, 60, 70, 80}},
		{"ties", 3 * time.Minute, time.Hour, []int64{31, 40, 50, 60, 70, 80}},
		{"empty", 2 * time.Hour, 3 * time.Hour, []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Non-UTC input must select the same stable UTC buckets.
			start := base.Add(tc.offset).In(time.FixedZone("offset", -7*60*60))
			ids, err := dashboardPollIDs(t.Context(), s.db, "app", start, base.Add(tc.end), 30*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if ids == nil || !slices.Equal(ids, tc.want) {
				t.Fatalf("ids=%v, want %v", ids, tc.want)
			}
		})
	}
}

func TestDashboardSeriesDenseAndNativeLatest(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := -1; i < 60; i++ {
		cpu := 0.1
		if i%30 == 15 {
			cpu = 0.9
		}
		dashboardTestPoll(t, s, int64(i+2), start.Add(time.Duration(i)*time.Minute), nil, map[string]float64{
			"http_requests_total": float64((i + 2) * 10), "http_request_time_total_seconds": float64(i + 2),
			"process_cpu_usage": cpu, "host_disk_used_bytes": float64(i + 2), "host_disk_total_bytes": 100,
		})
	}
	end := start.Add(59 * time.Minute) // Inclusive upper boundary, including native latest.
	native, err := s.Series(t.Context(), "app", start, end)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range []time.Duration{0, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour} {
		got, err := s.DashboardSeries(t.Context(), "app", start, end, bucket)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.LatestPoint, native.LatestPoint) || !reflect.DeepEqual(got.CurrentHostDisk, native.CurrentHostDisk) {
			t.Fatal("native latest values changed")
		}
		if bucket == 0 || bucket == 5*time.Minute {
			if !reflect.DeepEqual(got, native) {
				t.Fatal("short-range series changed")
			}
			continue
		}
		wantCount := 4
		if bucket == 2*time.Hour {
			wantCount = 2
		}
		if len(got.Points) != wantCount {
			t.Fatalf("sampled points=%d, want %d", len(got.Points), wantCount)
		}
		got = AggregateSeries(got, bucket)
		want := AggregateSeries(native, bucket)
		if len(got.Points) != len(want.Points) {
			t.Fatal("bucket count changed")
		}
		for i, p := range got.Points {
			w := want.Points[i]
			if !p.Timestamp.Equal(w.Timestamp) || *p.Requests != *w.Requests || math.Abs(*p.AverageLatencySeconds-*w.AverageLatencySeconds) > 1e-12 {
				t.Fatalf("bucket %d counter mismatch", i)
			}
			if *p.ProcessCPUUsage != 0.1 || *w.ProcessCPUUsage <= 0.1 {
				t.Fatal("expected coarse endpoint CPU average")
			}
		}
	}
}

func TestDashboardSeriesLatestMissingSamplesAndRestarts(t *testing.T) {
	for _, tc := range []string{"missing_latency", "missing_requests", "reset", "restart", "ties", "empty_endpoints", "latest_gauge_only", "no_samples"} {
		t.Run(tc, func(t *testing.T) {
			s := openTestStore(t)
			defer s.Close()
			start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			for i := 0; i < 6; i++ {
				at := start.Add(time.Duration(i) * time.Minute)
				if tc == "ties" && i >= 3 {
					at = start.Add(3 * time.Minute)
				}
				samples := map[string]float64{"http_requests_total": float64(100 + i*10), "http_request_time_total_seconds": float64(10 + i), "host_disk_used_bytes": float64(i + 1), "host_disk_total_bytes": 100}
				if tc == "missing_latency" && i == 3 {
					delete(samples, "http_request_time_total_seconds")
				}
				if tc == "missing_requests" && i == 3 {
					delete(samples, "http_requests_total")
				}
				if tc == "reset" && i == 4 {
					samples["http_requests_total"] = 2
				}
				var run *int64
				if tc == "restart" && i == 4 {
					id, err := s.EnsureAppRun(t.Context(), "app", &at, at)
					if err != nil {
						t.Fatal(err)
					}
					run = &id
				}
				if i == 5 || tc == "no_samples" || tc == "empty_endpoints" && i == 0 {
					samples = nil
				}
				if tc == "latest_gauge_only" && i == 4 {
					samples = map[string]float64{"process_cpu_usage": 0.3}
				}
				dashboardTestPoll(t, s, int64(i+1), at, run, samples)
			}
			end := start.Add(5 * time.Minute)
			want, err := s.Series(t.Context(), "app", start, end)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.DashboardSeries(t.Context(), "app", start, end, 30*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.LatestPoint, want.LatestPoint) || !reflect.DeepEqual(got.CurrentHostDisk, want.CurrentHostDisk) {
				t.Fatalf("native latest differs: got %+v want %+v", got.LatestPoint, want.LatestPoint)
			}
		})
	}
}

func TestDashboardSeriesSelectionCapFallback(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < dashboardPollLimit; i++ {
		stamp := formatSortableTime(start.Add(time.Duration(i) * time.Second))
		// Two endpoints per run exercise the endpoint cap, not just map size.
		run := i/2 + 1
		if i%2 == 0 {
			if _, err := tx.Exec(`INSERT INTO app_runs(id,target_id,first_seen_at,last_seen_at) VALUES(?,1,?,?)`, run, stamp, stamp); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO polls(id,target_id,app_run_id,started_at,finished_at,status) VALUES(?,1,?,?,?,'ok')`, i+1, run, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO metric_samples(poll_id,metric_key,metric_kind,value) VALUES(?,'http_requests_total','counter',?)`, i+1, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	end := start.Add(2 * time.Hour)
	ids, err := dashboardPollIDs(t.Context(), s.db, "app", start, end, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != dashboardPollLimit {
		t.Fatalf("ids=%d, want cap", len(ids))
	}
	// Replacing the last endpoint of an existing pair does not consume a slot.
	run := int64(dashboardPollLimit / 2)
	dashboardTestPoll(t, s, dashboardPollLimit+1, start.Add(dashboardPollLimit*time.Second), &run, map[string]float64{"http_requests_total": 901})
	ids, err = dashboardPollIDs(t.Context(), s.db, "app", start, end, 30*time.Minute)
	if err != nil || len(ids) != dashboardPollLimit || !slices.Contains(ids, int64(dashboardPollLimit+1)) {
		t.Fatalf("replacement: ids=%d err=%v", len(ids), err)
	}
	if _, err := s.DashboardSeries(t.Context(), "app", start, end, 30*time.Minute); err != nil {
		t.Fatalf("query at selected-ID cap: %v", err)
	}
	// A new null-run group would exceed the bound; the complete native path wins.
	dashboardTestPoll(t, s, dashboardPollLimit+2, start.Add((dashboardPollLimit+1)*time.Second), nil, map[string]float64{"http_requests_total": 902})
	ids, err = dashboardPollIDs(t.Context(), s.db, "app", start, end, 30*time.Minute)
	if err != nil || ids != nil {
		t.Fatalf("expected fallback, ids=%v err=%v", ids, err)
	}
	want, err := s.Series(t.Context(), "app", start, end)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.DashboardSeries(t.Context(), "app", start, end, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("fallback differs from raw series")
	}
}

// Run a writer after poll selection, before samples and counter baselines are
// read. The wrapper deliberately preserves the transaction used by production.
type dashboardConcurrentRead struct {
	*sql.Tx
	write func()
}

func (q *dashboardConcurrentRead) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if q.write != nil && strings.Contains(query, "ms.metric_key") {
		write := q.write
		q.write = nil
		write()
	}
	return q.Tx.QueryContext(ctx, query, args...)
}

func (q *dashboardConcurrentRead) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if q.write != nil {
		write := q.write
		q.write = nil
		write()
	}
	return q.Tx.QueryRowContext(ctx, query, args...)
}

func TestDashboardSeriesReadSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterTargets(t.Context(), []TargetIdentity{{Name: "app", Type: "spring"}}); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := -1; i < 4; i++ {
		dashboardTestPoll(t, s, int64(i+2), start.Add(time.Duration(i)*time.Minute), nil, map[string]float64{"http_requests_total": float64(100 + i*10), "host_disk_used_bytes": 10, "host_disk_total_bytes": 100})
	}
	end := start.Add(5 * time.Minute)
	want, err := s.DashboardSeries(t.Context(), "app", start, end, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	tx, err := s.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	q := &dashboardConcurrentRead{Tx: tx, write: func() {
		// Simulate retention removing the pre-window baseline and collection
		// changing observations while this request is being assembled.
		if _, err := writer.Exec(`DELETE FROM metric_samples WHERE poll_id=1; UPDATE metric_samples SET value=value+1000 WHERE poll_id=5`); err != nil {
			t.Fatal(err)
		}
	}}
	got, err := s.dashboardSeries(t.Context(), q, "app", start, end, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if q.write != nil {
		t.Fatal("writer was not exercised")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("mixed read snapshots")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	after, err := s.DashboardSeries(t.Context(), "app", start, end, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(after, want) {
		t.Fatal("writer did not change the database")
	}
}

func TestDashboardSeriesEmptySparseAndErrors(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	got, err := s.DashboardSeries(t.Context(), "missing", start, end, 30*time.Minute)
	if err != nil || len(got.Points) != 0 || got.LatestPoint != nil || got.CurrentHostDisk != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
	for i := 0; i < 3; i++ {
		dashboardTestPoll(t, s, int64(i+1), start.Add(time.Duration(i)*30*time.Minute), nil, map[string]float64{"http_requests_total": float64(i * 10)})
	}
	want, err := s.Series(t.Context(), "app", start, end)
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.DashboardSeries(t.Context(), "app", start, end, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(AggregateSeries(got, 30*time.Minute), want) {
		t.Fatal("sparse series changed")
	}
	for _, tc := range []struct {
		target     string
		start, end time.Time
	}{{" ", start, end}, {"app", end, start}, {"app", end, end}} {
		if _, err := s.DashboardSeries(t.Context(), tc.target, tc.start, tc.end, 30*time.Minute); err == nil {
			t.Fatal("expected validation error")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.DashboardSeries(ctx, "app", start, end, 30*time.Minute); err == nil {
		t.Fatal("expected cancellation")
	}
	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("connection leaked: %v", err)
	}
}
