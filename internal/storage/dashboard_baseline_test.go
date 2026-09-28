package storage

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"
)

type dashboardBaselineReads struct {
	seriesQuerier
	args [][]any
}

func (q *dashboardBaselineReads) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if strings.Contains(query, "AND ms.metric_key = ?") {
		q.args = append(q.args, append([]any(nil), args...))
	}
	return q.seriesQuerier.QueryRowContext(ctx, query, args...)
}

func TestDashboardLatestReusesPreWindowBaseline(t *testing.T) {
	for _, scenario := range []string{"mixed_baselines", "restart", "all_counters_absent", "one_timestamp"} {
		t.Run(scenario, func(t *testing.T) {
			s := openTestStore(t)
			defer s.Close()
			start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			latest := start.Add(4 * time.Minute)
			if scenario == "one_timestamp" {
				latest = start
			}
			polls := []struct {
				at     time.Time
				values map[string]float64
			}{
				{start.Add(-time.Hour), map[string]float64{"http_requests_total": 100, "http_request_time_total_seconds": 10, "http_404_total": 3, "http_4xx_total": 5}},
				{start, map[string]float64{"http_requests_total": 110, "http_request_time_total_seconds": 11}},
				{start.Add(time.Minute), map[string]float64{"http_requests_total": 120, "http_request_time_total_seconds": 12, "http_404_total": 7}},
				{latest, map[string]float64{"http_requests_total": 140, "http_request_time_total_seconds": 14, "http_404_total": 9, "http_4xx_total": 8}},
			}
			var run *int64
			if scenario == "restart" {
				id, err := s.EnsureAppRun(t.Context(), "app", &start, start)
				if err != nil {
					t.Fatal(err)
				}
				run = &id
			}
			for i, p := range polls {
				if scenario == "one_timestamp" && i > 0 {
					p.at = start
				}
				if scenario == "all_counters_absent" {
					p.values = map[string]float64{}
				}
				p.values["process_cpu_usage"] = 0.1
				pollRun := run
				if i == 0 {
					pollRun = nil
				}
				dashboardTestPoll(t, s, int64(i+1), p.at, pollRun, p.values)
			}
			want, err := s.Series(t.Context(), "app", start, latest.Add(time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			tx, err := s.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			q := &dashboardBaselineReads{seriesQuerier: tx}
			got, err := s.dashboardSeries(t.Context(), q, "app", start, latest.Add(time.Minute), 30*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.LatestPoint, want.LatestPoint) {
				t.Fatalf("latest differs: got %+v want %+v", got.LatestPoint, want.LatestPoint)
			}
			if scenario == "mixed_baselines" && (*got.LatestPoint.Requests != 20 || *got.LatestPoint.HTTP404 != 2 || *got.LatestPoint.HTTP4xx != 3 || got.LatestPoint.HTTP5xx != nil) {
				t.Fatal("latest did not combine in-window, pre-window, and absent counter baselines")
			}
			wantLookups := 2 * len(seriesCounterKeys)
			if scenario == "one_timestamp" {
				wantLookups = len(seriesCounterKeys)
			}
			if len(q.args) != wantLookups {
				t.Fatalf("baseline lookups=%d, want %d", len(q.args), wantLookups)
			}
			for i, args := range q.args {
				if i < len(seriesCounterKeys) {
					if len(args) != 4 || args[1] != formatSortableTime(start) {
						t.Fatalf("unexpected initial baseline: %v", args)
					}
				} else if len(args) != 5 || args[1] != formatSortableTime(latest) || args[4] != formatSortableTime(start) {
					t.Fatalf("native latest baseline escaped the requested window: %v", args)
				}
			}
		})
	}
}

func TestDashboardThirtyDaysWithFailedPollsStaysSampled(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	run, err := s.EnsureAppRun(t.Context(), "app", &start, start)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const buckets = 30 * 12
	for bucket := 0; bucket < buckets; bucket++ {
		for i, offset := range []time.Duration{0, 30 * time.Second, time.Hour, 2*time.Hour - time.Minute, 2*time.Hour - 30*time.Second} {
			id := bucket*5 + i + 1
			stamp := formatSortableTime(start.Add(time.Duration(bucket)*2*time.Hour + offset))
			var pollRun any = run
			status := "ok"
			if i == 1 || i == 3 {
				pollRun, status = nil, "error"
			}
			if _, err := tx.Exec(`INSERT INTO polls(id,target_id,app_run_id,started_at,finished_at,status) VALUES(?,1,?,?,?,?)`, id, pollRun, stamp, stamp, status); err != nil {
				t.Fatal(err)
			}
			if status == "ok" {
				if _, err := tx.Exec(`INSERT INTO metric_samples(poll_id,metric_key,metric_kind,value) VALUES(?,'http_requests_total','counter',?)`, id, id); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	end := start.Add(30 * 24 * time.Hour)
	ids, err := dashboardPollIDs(t.Context(), s.db, "app", start, end, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 4*buckets {
		t.Fatalf("selected %d IDs, want %d including failed polls", len(ids), 4*buckets)
	}
	got, err := s.DashboardSeries(t.Context(), "app", start, end, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Points) != 2*buckets {
		t.Fatalf("loaded %d points; selection must not fall back to all %d successful polls", len(got.Points), 3*buckets)
	}
}
