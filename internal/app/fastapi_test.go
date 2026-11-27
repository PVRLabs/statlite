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

func TestFastAPIMonitorWiring(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "reader" || pass != "secret" || r.URL.RequestURI() != "/custom?source=app" {
			http.Error(w, "unexpected scrape", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `http_request_duration_highr_seconds_count 4
http_request_duration_highr_seconds_sum 0.4
http_requests_total{handler="none",method="GET",status="404"} 3
http_request_duration_seconds_count{handler="none",method="GET"} 3
http_request_duration_seconds_sum{handler="none",method="GET"} 0.3
http_requests_total{handler="/custom",method="GET",status="200"} 1
http_request_duration_seconds_count{handler="/custom",method="GET"} 1
http_request_duration_seconds_sum{handler="/custom",method="GET"} 0.1
`)
	}))
	defer server.Close()

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "statlite.yaml")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`server:
  listen: 127.0.0.1:9090
storage:
  sqlite_path: ./statlite.sqlite
polling:
  interval: 1m
targets:
  - name: app
    type: fastapi
    url: %q
    auth:
      type: basic
      username: reader
      password: secret
`, server.URL+"/custom?source=app")), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	store, err := storage.Open(ctx, filepath.Join(dir, "statlite.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.RegisterTargets(ctx, []storage.TargetIdentity{{Name: "app", Type: config.TargetTypeFastAPI}}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewMonitorManager(cfg.Targets, store, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	target, ok := manager.ExactTarget("app")
	if !ok || target.Monitor.IntegrationType() != config.TargetTypeFastAPI {
		t.Fatalf("target = %+v", target)
	}
	snapshot, err := target.Monitor.PollNow(ctx)
	if err != nil || snapshot.AppRunID == nil || snapshot.Result.MetricsSource != collector.FastAPIMetricsSource {
		t.Fatalf("poll = %+v, %v", snapshot, err)
	}
	expected := map[string]float64{"http_requests_total": 3, "http_404_total": 3, "http_4xx_total": 3,
		"http_5xx_total": 0, "http_request_time_total_seconds": 0.3}
	for _, sample := range snapshot.Result.Samples {
		want, ok := expected[sample.Key]
		if !ok || sample.Value != want {
			t.Fatalf("unexpected sample %+v", sample)
		}
		delete(expected, sample.Key)
	}
	if len(expected) != 0 {
		t.Fatalf("missing samples: %v", expected)
	}
}
