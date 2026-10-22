package monitor

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/prometheus"
	"github.com/pvrlabs/statlite/internal/storage"
)

// Exercise the production adapter with deterministic poll timestamps so both
// sampled paths place multiple polls and run boundaries in the same bucket.
type timedCaddyCollector struct {
	collector *collector.CaddyCollector
	at        time.Time
}

func (c *timedCaddyCollector) Collect(ctx context.Context) (*collector.CollectionResult, error) {
	result, err := c.collector.Collect(ctx)
	c.at = c.at.Add(time.Minute)
	result.PollStartedAt, result.PollFinishedAt = c.at, c.at
	return result, err
}

func caddyPopulationBody(server string, counts, sums []float64) string {
	body := ""
	for i, count := range counts {
		labels := fmt.Sprintf(`host="app.test",handler="subroute",server=%q,method="GET",code="%d"`, server, 200+i*100)
		body += fmt.Sprintf("caddy_http_request_duration_seconds_count{%s} %g\ncaddy_http_request_duration_seconds_sum{%s} %g\n", labels, count, labels, sums[i])
	}
	return body
}

func TestCaddyStoredContinuity(t *testing.T) {
	for _, mode := range []string{"hidden_reset", "sum_reset", "disappearance", "fingerprint_change", "scrape_failure", "missing_data", "invalid_data", "recreation", "monitor_recreation", "collector_recreation", "write_failure", "reload", "process_restart"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			path := filepath.Join(t.TempDir(), "caddy.sqlite")
			store, err := storage.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.RegisterTargets(ctx, []storage.TargetIdentity{{Name: "app", Type: config.TargetTypeCaddy}}); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			body := caddyPopulationBody("srv0", []float64{100, 100}, []float64{10, 10})
			status := http.StatusOK
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); fmt.Fprint(w, body) }))
			defer server.Close()
			newCollector := func() *collector.CaddyCollector {
				client, err := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
				if err != nil {
					t.Fatal(err)
				}
				return collector.NewCaddyCollector("app", server.URL, "app.test", client)
			}
			base := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
			c := &timedCaddyCollector{collector: newCollector(), at: base}
			mon := newTestMonitor(t, store, c)
			if got := newTestMonitor(t, store, c.collector).IntegrationType(); got != config.TargetTypeCaddy {
				t.Fatalf("integration = %q", got)
			}
			poll := func() *storage.Snapshot {
				t.Helper()
				snap, err := mon.PollNow(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if snap.AppRunID == nil || snap.Result.ProcessStartTime != nil || snap.Result.HealthStatus != "" || snap.Result.DBHealthStatus != "" {
					t.Fatalf("invalid lifecycle: %+v", snap)
				}
				if len(snap.Result.Samples) != 5 {
					t.Fatalf("samples = %v", snap.Result.Samples)
				}
				return snap
			}
			first := poll()
			body = caddyPopulationBody("srv0", []float64{110, 110}, []float64{11, 11})
			before := poll()
			if *first.AppRunID != *before.AppRunID {
				t.Fatal("normal traffic changed run")
			}
			counts, sums, identity := []float64{150, 151}, []float64{15, 15.1}, "srv0"
			switch mode {
			case "hidden_reset", "write_failure":
				counts, sums = []float64{1, 300}, []float64{0.1, 30}
			case "sum_reset":
				counts, sums = []float64{150, 151}, []float64{1, 30}
			case "disappearance":
				counts, sums = []float64{301}, []float64{30.1}
			case "fingerprint_change":
				identity = "srv1"
			case "reload", "process_restart":
				counts, sums = []float64{1, 2}, []float64{0.1, 0.2}
			case "scrape_failure", "missing_data", "invalid_data":
				body = ""
				if mode == "scrape_failure" {
					status = 503
				}
				if mode == "invalid_data" {
					body = caddyPopulationBody("srv0", []float64{1.5}, []float64{1})
				}
				if _, err := mon.PollNow(ctx); err == nil {
					t.Fatal("expected collection failure")
				}
				if mon.Status().ConsecutivePollFailures != 1 {
					t.Fatal("failure not recorded")
				}
				status = 200
			case "recreation":
				c.collector = newCollector()
				mon = newTestMonitor(t, store, c)
			case "monitor_recreation":
				mon = newTestMonitor(t, store, c)
			case "collector_recreation":
				c.collector = newCollector()
			}
			body = caddyPopulationBody(identity, counts, sums)
			if mode == "write_failure" {
				if _, err := db.Exec(`CREATE TRIGGER caddy_fail BEFORE INSERT ON polls BEGIN SELECT RAISE(FAIL,'controlled write failure'); END`); err != nil {
					t.Fatal(err)
				}
				if _, err := mon.PollNow(ctx); err == nil {
					t.Fatal("expected write failure")
				}
				if mon.Status().ConsecutivePollFailures != 1 {
					t.Fatal("write failure not recorded")
				}
				if _, err := db.Exec(`DROP TRIGGER caddy_fail`); err != nil {
					t.Fatal(err)
				}
			}
			baseline := poll()
			if *baseline.AppRunID == *before.AppRunID {
				t.Fatal("break reused prior run")
			}
			// All raw cumulative values survive unchanged, including a hidden reset.
			raw, ok := sampleValue(baseline.Result.Samples, "http_requests_total")
			wantRaw := 0.0
			for _, n := range counts {
				wantRaw += n
			}
			if !ok || raw != wantRaw {
				t.Fatalf("raw count = %v, want %v", raw, wantRaw)
			}
			rawSum, ok := sampleValue(baseline.Result.Samples, "http_request_time_total_seconds")
			wantSum := 0.0
			for _, n := range sums {
				wantSum += n
			}
			if !ok || math.Abs(rawSum-wantSum) > 1e-9 {
				t.Fatalf("raw duration = %v, want %v", rawSum, wantSum)
			}
			counts[0] += 11
			sums[0] += 1.1
			body = caddyPopulationBody(identity, counts, sums)
			last := poll()
			if *last.AppRunID != *baseline.AppRunID {
				t.Fatal("recovery failed to retain run")
			}
			start, end := base, base.Add(24*time.Hour)
			check := func(label string, series *storage.Series) {
				t.Helper()
				total := 0.0
				latencyObserved := false
				for _, p := range series.Points {
					if p.Requests != nil {
						total += *p.Requests
					}
					if p.AverageLatencySeconds != nil {
						latencyObserved = true
						if math.Abs(*p.AverageLatencySeconds-0.1) > 1e-9 {
							t.Fatalf("%s latency = %v", label, *p.AverageLatencySeconds)
						}
					}
				}
				if total != 31 || !latencyObserved {
					t.Fatalf("%s total=%v latencyObserved=%v", label, total, latencyObserved)
				}
			}
			series, err := mon.Series(ctx, start, end)
			if err != nil {
				t.Fatal(err)
			}
			check("short", series)
			for _, p := range series.Points {
				if p.Timestamp.Equal(first.Result.PollStartedAt) || p.Timestamp.Equal(baseline.Result.PollStartedAt) {
					if p.Requests != nil || p.AverageLatencySeconds != nil {
						t.Fatal("baseline has delta")
					}
				}
			}
			for _, bucket := range []time.Duration{30 * time.Minute, 2 * time.Hour} {
				sampled, err := mon.DashboardSeries(ctx, start, end, bucket)
				if err != nil {
					t.Fatal(err)
				}
				check("sampled", sampled)
				check("aggregated", storage.AggregateSeries(sampled, bucket))
			}
			bounded, err := mon.BoundedSeries(ctx, start, end, start)
			if err != nil {
				t.Fatal(err)
			}
			public := storage.GroupPublicMetrics(bounded)
			total := 0.0
			for _, p := range public.Points {
				if p.Requests != nil {
					total += *p.Requests
				}
				if p.AverageLatencySeconds != nil && math.Abs(*p.AverageLatencySeconds-0.1) > 1e-9 {
					t.Fatal("public latency crossed boundary")
				}
			}
			if total != 31 {
				t.Fatalf("public total=%v", total)
			}
			manager, err := NewManager([]ManagedTarget{{Metadata: TargetMetadata{Name: "app", Type: config.TargetTypeCaddy}, Monitor: mon}})
			if err != nil {
				t.Fatal(err)
			}
			summary := manager.Summaries()[0]
			if summary.Latest.PollID != last.PollID || summary.Latest.Result.ProcessStartTime != nil || summary.Latest.Result.HealthStatus != "" || summary.Status.ConsecutivePollFailures != 0 {
				t.Fatalf("summary=%+v", summary)
			}
			_, _, health := mon.CurrentState()
			if health.Reported {
				t.Fatal("invented health")
			}
			events, err := mon.Events(ctx, start, end, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, event := range events {
				if event.Type == EventTypeRestartDetected {
					t.Fatal("invented restart event")
				}
			}
			var started int
			if err := db.QueryRow(`SELECT count(*) FROM app_runs WHERE process_start_time IS NOT NULL`).Scan(&started); err != nil {
				t.Fatal(err)
			}
			if started != 0 {
				t.Fatal("stored process start")
			}
		})
	}
}

func TestCaddyNewTuplesAndIdleKeepRun(t *testing.T) {
	store := openTestStore(t)
	defer store.Close()
	body := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
	defer server.Close()
	client, err := prometheus.NewClient(time.Second, prometheus.DefaultLimits, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	c := &timedCaddyCollector{collector: collector.NewCaddyCollector("app", server.URL, "app.test", client), at: base}
	mon := newTestMonitor(t, store, c)
	if _, err := mon.PollNow(t.Context()); err == nil {
		t.Fatal("initial missing population should fail")
	}
	var run int64
	for i, counts := range [][]float64{{100}, {110}, {110, 5}, {110, 5}} {
		sums := make([]float64, len(counts))
		for j, n := range counts {
			sums[j] = n / 10
		}
		body = caddyPopulationBody("srv0", counts, sums)
		snap, err := mon.PollNow(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			run = *snap.AppRunID
		} else if *snap.AppRunID != run {
			t.Fatal("new tuple or idle changed run")
		}
	}
	series, err := mon.Series(t.Context(), base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(series.Points) != 4 {
		t.Fatalf("points = %d", len(series.Points))
	}
	for i, want := range []float64{10, 5, 0} {
		p := series.Points[i+1]
		if p.Requests == nil || *p.Requests != want {
			t.Fatalf("point %d requests = %v", i, p.Requests)
		}
		if want == 0 {
			if p.AverageLatencySeconds != nil {
				t.Fatal("idle latency should be unavailable")
			}
		} else if p.AverageLatencySeconds == nil || math.Abs(*p.AverageLatencySeconds-0.1) > 1e-9 {
			t.Fatal("new tuple latency mismatch")
		}
	}
	if series.Points[0].Requests != nil {
		t.Fatal("missing data or first baseline synthesized delta")
	}
}
