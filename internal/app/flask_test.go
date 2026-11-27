package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestFlaskStoredLatencyGap(t *testing.T) {
	type poll struct {
		ok, failures int
		okPath       string
	}
	input := make(chan poll, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		p := <-input
		fmt.Fprint(w, "flask_exporter_info{version=\"0.23.2\"} 1\nprocess_start_time_seconds 100\n")
		for _, route := range []struct {
			path, status string
			count        int
		}{{p.okPath, "200", p.ok}, {"/crash", "500", p.failures}} {
			fmt.Fprintf(w, "flask_http_request_total{method=\"GET\",status=%q} %d\n", route.status, route.count)
			for _, field := range []string{"count", "sum"} {
				fmt.Fprintf(w, "flask_http_request_duration_seconds_%s{method=\"GET\",status=%q,path=%q} %d\n",
					field, route.status, route.path, route.count)
			}
		}
		fmt.Fprintf(w, "flask_http_request_exceptions_total{method=\"GET\",status=\"500\"} %d\n", p.failures)
	}))
	defer server.Close()

	ctx := t.Context()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "statlite.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterTargets(ctx, []storage.TargetIdentity{{Name: "app", Type: config.TargetTypeFlask}}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewMonitorManager([]config.TargetConfig{{Name: "app", Type: config.TargetTypeFlask, URL: server.URL + "/metrics"}}, store, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := manager.ExactTarget("app")
	if !ok || target.Monitor.IntegrationType() != config.TargetTypeFlask {
		t.Fatalf("target = %+v", target)
	}
	start := time.Now().Add(-time.Second)
	for _, step := range []poll{{1, 0, "/ok"}, {9, 2, "/ok"}, {9, 2, "/ok"}, {10, 2, "/other"}, {11, 2, "/other"}, {12, 2, "/other"}} {
		input <- step
		p, err := target.Monitor.PollNow(ctx)
		if err != nil || p.Result.MetricsSource != collector.FlaskMetricsSource {
			t.Fatalf("poll = %+v, error = %v", p, err)
		}
	}
	series, err := target.Monitor.Series(ctx, start, time.Now().Add(time.Second))
	if err != nil || len(series.Points) != 6 {
		t.Fatalf("series = %+v, error = %v", series, err)
	}
	for i, want := range []struct {
		requests, errors float64
		latency          bool
	}{{10, 2, true}, {0, 0, false}, {1, 0, false}, {1, 0, false}, {1, 0, true}} {
		p := series.Points[i+1]
		if p.Requests == nil || *p.Requests != want.requests || p.HTTP5xx == nil || *p.HTTP5xx != want.errors ||
			(p.AverageLatencySeconds != nil) != want.latency || want.latency && *p.AverageLatencySeconds != 1 {
			t.Fatalf("point %d = %+v, want %+v", i+1, p, want)
		}
	}
}
