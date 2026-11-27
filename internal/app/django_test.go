package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestDjangoStoredLatencyGap(t *testing.T) {
	type counts struct{ responses, crashes, duration int }
	payload := make(chan counts, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s := <-payload
		fmt.Fprintf(w, `django_http_requests_before_middlewares_total %d
django_http_responses_total_by_status_total{status="200"} %d
django_http_responses_total_by_status_total{status="500"} %d
django_http_requests_latency_including_middlewares_seconds_count %d
django_http_requests_latency_including_middlewares_seconds_sum %d
process_start_time_seconds 1
`, s.responses, s.responses-s.crashes, s.crashes, s.duration, s.duration)
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "statlite.yaml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(`server:
  listen: 127.0.0.1:9090
storage:
  sqlite_path: ./statlite.sqlite
polling:
  interval: 1m
targets:
  - name: app
    type: django
    url: %q
    django_metrics_mode: separate
`, server.URL+"/metrics")), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	store, err := storage.Open(ctx, filepath.Join(dir, "statlite.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterTargets(ctx, []storage.TargetIdentity{{Name: "app", Type: config.TargetTypeDjango}}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewMonitorManager(cfg.Targets, store, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := manager.ExactTarget("app")
	if !ok || target.Monitor.IntegrationType() != config.TargetTypeDjango {
		t.Fatalf("target = %+v", target)
	}
	start := time.Now().Add(-time.Second)
	for _, step := range []counts{{1, 0, 1}, {11, 2, 12}, {12, 2, 12}, {13, 2, 13}} {
		payload <- step
		poll, err := target.Monitor.PollNow(ctx)
		if err != nil || poll.Result.MetricsSource != collector.DjangoMetricsSource {
			t.Fatalf("poll = %+v, error = %v", poll, err)
		}
	}
	series, err := target.Monitor.Series(ctx, start, time.Now().Add(time.Second))
	if err != nil || len(series.Points) != 4 {
		t.Fatalf("series = %+v, error = %v", series, err)
	}
	mixed, rebaseline, recovered := series.Points[1], series.Points[2], series.Points[3]
	if mixed.Requests == nil || *mixed.Requests != 10 || mixed.HTTP5xx == nil || *mixed.HTTP5xx != 2 ||
		mixed.AverageLatencySeconds != nil || rebaseline.Requests == nil || *rebaseline.Requests != 1 ||
		rebaseline.AverageLatencySeconds != nil || recovered.AverageLatencySeconds == nil || *recovered.AverageLatencySeconds != 1 {
		t.Fatalf("unexpected stored points: mixed=%+v rebaseline=%+v recovered=%+v", mixed, rebaseline, recovered)
	}
}
