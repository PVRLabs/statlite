package storage

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// Long-range chart counters intentionally approximate the raw history. These
// fixtures document accepted differences; native latest values must still match.
func TestDashboardSeriesAcceptedCounterApproximations(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		minutes                 []int
		requests, latency       []float64 // -1 means missing
		wantNative, wantSampled []*float64
	}{
		{"same_run_reset", []int{0, 1, 2, 3}, []float64{100, 120, 5, 150}, nil,
			[]*float64{edgeNumber(165)}, []*float64{edgeNumber(50)}},
		{"missing_latency", []int{0, 1, 2, 3}, []float64{100, 110, 120, 130}, []float64{10, -1, 16, 17},
			[]*float64{edgeNumber(30)}, []*float64{edgeNumber(30)}},
		{"missing_bucket_endpoint", []int{0, 5, 29, 31}, []float64{100, 110, -1, 130}, nil,
			[]*float64{edgeNumber(10), edgeNumber(20)}, []*float64{nil, edgeNumber(30)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := openTestStore(t)
			defer s.Close()
			ctx := context.Background()
			start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			end := start.Add(time.Hour)
			if _, err := s.db.Exec(`INSERT INTO targets(id,name,created_at) VALUES(123,'edge',?)`, formatSortableTime(start)); err != nil {
				t.Fatal(err)
			}
			for i, minute := range tc.minutes {
				stamp := formatSortableTime(start.Add(time.Duration(minute) * time.Minute))
				if _, err := s.db.Exec(`INSERT INTO polls(id,target_id,started_at,finished_at,status) VALUES(?,123,?,?,'ok')`, i+1, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				samples := map[string]float64{"http_requests_total": tc.requests[i], "process_cpu_usage": 0.5}
				if tc.latency != nil {
					samples["http_request_time_total_seconds"] = tc.latency[i]
				}
				for key, value := range samples {
					if value < 0 {
						continue
					}
					kind := "counter"
					if key == "process_cpu_usage" {
						kind = "gauge"
					}
					if _, err := s.db.Exec(`INSERT INTO metric_samples(poll_id,metric_key,metric_kind,value) VALUES(?,?,?,?)`, i+1, key, kind, value); err != nil {
						t.Fatal(err)
					}
				}
			}
			bucket := 30 * time.Minute
			native, err := s.Series(ctx, "edge", start, end)
			if err != nil {
				t.Fatal(err)
			}
			sampled, err := s.DashboardSeries(ctx, "edge", start, end, bucket)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(sampled.LatestPoint, native.LatestPoint) {
				t.Fatal("native latest changed")
			}
			native = AggregateSeries(native, bucket)
			sampled = AggregateSeries(sampled, bucket)
			for label, check := range map[string]struct {
				series *Series
				want   []*float64
			}{
				"native": {native, tc.wantNative}, "sampled": {sampled, tc.wantSampled},
			} {
				got := make([]*float64, len(check.series.Points))
				for i, point := range check.series.Points {
					got[i] = point.Requests
				}
				if !reflect.DeepEqual(got, check.want) {
					t.Fatalf("%s requests = %v, want %v", label, got, check.want)
				}
				for i, point := range check.series.Points {
					t.Logf("%s bucket %d: requests=%s latency=%s", label, i, edgeValue(point.Requests), edgeValue(point.AverageLatencySeconds))
				}
			}
			if tc.name == "missing_latency" {
				if native.Points[0].AverageLatencySeconds == nil || *native.Points[0].AverageLatencySeconds != 0.1 {
					t.Fatal("unexpected native latency")
				}
				if sampled.Points[0].AverageLatencySeconds == nil || *sampled.Points[0].AverageLatencySeconds != 7.0/30 {
					t.Fatal("unexpected sampled latency")
				}
			}
		})
	}
}

func edgeNumber(v float64) *float64 { return &v }

func edgeValue(v *float64) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%.6g", *v)
}
