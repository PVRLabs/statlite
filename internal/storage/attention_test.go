package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAttentionHTTP5xx(t *testing.T) {
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		change           func(int, map[string]float64, *int64) (skip bool)
		requests, errors float64
		adequate         bool
	}{
		{name: "normal", requests: 200, errors: 10, adequate: true},
		{name: "elevated", change: func(i int, m map[string]float64, _ *int64) bool {
			m["http_5xx_total"] = 100 + float64(i*4)
			return false
		}, requests: 200, errors: 40, adequate: true},
		{name: "zero traffic", change: func(_ int, m map[string]float64, _ *int64) bool {
			m["http_requests_total"] = 0
			m["http_5xx_total"] = 0
			return false
		}, adequate: true},
		{name: "stale", change: func(i int, _ map[string]float64, _ *int64) bool { return i > 7 }, requests: 140, errors: 7},
		{name: "latest missing", change: func(i int, m map[string]float64, _ *int64) bool {
			if i == 10 {
				delete(m, "http_5xx_total")
			}
			return false
		}, requests: 180, errors: 9},
		{name: "mismatched baseline", change: func(i int, m map[string]float64, _ *int64) bool {
			if i == 5 {
				delete(m, "http_5xx_total")
			}
			return false
		}, requests: 160, errors: 8, adequate: true},
		{name: "request reset", change: func(i int, m map[string]float64, _ *int64) bool {
			if i >= 5 {
				m["http_requests_total"] -= 100
			}
			return false
		}, requests: 180, errors: 9, adequate: true},
		{name: "error reset", change: func(i int, m map[string]float64, _ *int64) bool {
			if i >= 5 {
				m["http_5xx_total"] -= 5
			}
			return false
		}, requests: 180, errors: 9, adequate: true},
		{name: "restart", change: func(i int, _ map[string]float64, run *int64) bool {
			if i >= 5 {
				*run = 2
			}
			return false
		}, requests: 180, errors: 9, adequate: true},
		{name: "impossible count", change: func(i int, m map[string]float64, _ *int64) bool {
			if i >= 5 {
				m["http_5xx_total"] += 30
			}
			return false
		}, requests: 180, errors: 9, adequate: true},
		{name: "long gap", change: func(i int, _ map[string]float64, _ *int64) bool { return i >= 4 && i <= 6 }, requests: 120, errors: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			defer s.Close()
			for i := range 2 {
				at := base.Add(-time.Duration(i+2) * time.Hour)
				id, err := s.EnsureAppRun(t.Context(), "app", &at, at)
				if err != nil || id != int64(i+1) {
					t.Fatalf("run %d: %v", id, err)
				}
			}
			for i := -1; i <= 11; i++ {
				values := map[string]float64{"http_requests_total": 1000 + float64(i*20), "http_5xx_total": 100 + float64(i)}
				run := int64(1)
				if i >= 0 && i <= 10 && tc.change != nil && tc.change(i, values, &run) {
					continue
				}
				dashboardTestPoll(t, s, int64(i+2), base.Add(time.Duration(i)*time.Minute), &run, values)
			}
			got, err := s.AttentionHTTP5xx(t.Context(), "app", base, base.Add(10*time.Minute), time.Minute)
			want := HTTPErrorWindow{Requests: tc.requests, Errors: tc.errors, Adequate: tc.adequate}
			// Each contributing fixture interval adds one request delta of 20.
			want.Covered = time.Duration(tc.requests/20) * time.Minute
			if tc.name == "zero traffic" {
				want.Covered = 10 * time.Minute
			}
			// The fixture includes out-of-window polls. They must never contribute.
			if err != nil || got != want {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

func TestAttentionSlowPolling(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cadence time.Duration
		want    HTTPErrorWindow
	}{
		{"three minutes", 3 * time.Minute, HTTPErrorWindow{Requests: 200, Errors: 20, Adequate: true, Covered: 6 * time.Minute}},
		{"five minutes", 5 * time.Minute, HTTPErrorWindow{Requests: 100, Errors: 10, Adequate: true, Covered: 5 * time.Minute}},
		{"longer than five minutes", 6 * time.Minute, HTTPErrorWindow{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			defer s.Close()
			start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
			end := start.Add(10 * time.Minute)
			// Offset the first poll to exercise a partial interval at both edges.
			for i, at := 0, start.Add(-30*time.Second); !at.After(end); i, at = i+1, at.Add(tc.cadence) {
				dashboardTestPoll(t, s, int64(i+1), at, nil, map[string]float64{
					"http_requests_total": float64(i * 100),
					"http_5xx_total":      float64(i * 10),
				})
			}
			got, err := s.AttentionHTTP5xx(t.Context(), "app", start, end, tc.cadence)
			if err != nil || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestAttentionQueryCancellationAndIndexes(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	now := time.Now()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.AttentionHTTP5xx(ctx, "app", now.Add(-10*time.Minute), now, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	rows, err := s.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+attentionPollQuery, "app", formatSortableTime(now.Add(-10*time.Minute)), formatSortableTime(now), attentionPollLimit+1)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	t.Log(plan.String())
	if !strings.Contains(plan.String(), "idx_polls_target_started_at") || !strings.Contains(plan.String(), "idx_metric_samples_poll_id") {
		t.Fatal("expected bounded indexed reads")
	}
}
