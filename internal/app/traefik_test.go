package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/storage"
)

func TestTraefikMonitorWiring(t *testing.T) {
	const labels = `{service="alpha@file",method="GET",protocol="http",code="404"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "reader" || pass != "secret" || r.URL.RequestURI() != "/custom?source=edge" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "traefik_service_requests_total"+labels+" 3")
		fmt.Fprintln(w, "traefik_service_request_duration_seconds_count"+labels+" 3")
		fmt.Fprintln(w, "traefik_service_request_duration_seconds_sum"+labels+" 0.3")
	}))
	defer server.Close()
	ctx := t.Context()
	store, err := storage.Open(ctx, filepath.Join(t.TempDir(), "statlite.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	target := config.TargetConfig{
		Name: "edge", Type: config.TargetTypeTraefik, URL: server.URL + "/custom?source=edge", Service: "alpha@file",
		Auth: &config.AuthConfig{Type: "basic", Username: "reader", Password: "secret"},
	}
	if err := store.RegisterTargets(ctx, []storage.TargetIdentity{{Name: target.Name, Type: target.Type}}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewMonitorManager([]config.TargetConfig{target}, store, time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	managed, ok := manager.ExactTarget("edge")
	if !ok || managed.Metadata.Type != config.TargetTypeTraefik || managed.Metadata.EndpointSource != "url" {
		t.Fatalf("target = %+v", managed)
	}
	snap, err := managed.Monitor.PollNow(ctx)
	if err != nil || snap.AppRunID == nil || snap.Result.ProcessStartTime != nil || len(snap.Result.Samples) != 5 {
		t.Fatalf("poll = %+v, %v", snap, err)
	}
	for _, sample := range snap.Result.Samples {
		switch sample.Key {
		case "http_requests_total", "http_404_total", "http_4xx_total":
			if sample.Value != 3 {
				t.Fatalf("status sample = %+v", sample)
			}
		case "http_5xx_total":
			if sample.Value != 0 {
				t.Fatalf("5xx = %+v", sample)
			}
		case "http_request_time_total_seconds":
			if sample.Value != 0.3 {
				t.Fatalf("duration = %+v", sample)
			}
		default:
			t.Fatalf("unexpected sample = %+v", sample)
		}
	}
}
