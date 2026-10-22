package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
)

func TestCaddyCollectorWiring(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "user" || password != "secret" || r.URL.RequestURI() != "/custom/metrics?source=caddy" {
			http.Error(w, "unexpected request", 400)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, `caddy_http_request_duration_seconds_count{host="alpha.test",handler="subroute",server="srv1",method="GET",code="502"} 3`)
		fmt.Fprintln(w, `caddy_http_request_duration_seconds_sum{host="alpha.test",handler="subroute",server="srv1",method="GET",code="502"} 0.3`)
	}))
	defer server.Close()
	c, err := newCollector(config.TargetConfig{Name: "app", Type: config.TargetTypeCaddy, URL: server.URL + "/custom/metrics?source=caddy", Host: "alpha.test", Auth: &config.AuthConfig{Type: "basic", Username: "user", Password: "secret"}}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.(*collector.CaddyCollector); !ok {
		t.Fatalf("collector: %T", c)
	}
	r, err := c.Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.TargetName != "app" || r.MetricsSource != collector.CaddyMetricsSource || len(r.Samples) != 5 || r.Samples[0].Value != 3 || r.Samples[3].Value != 3 || r.HealthStatus != "" || r.ProcessStartTime != nil {
		t.Fatalf("result: %+v", r)
	}
}
func TestCaddyCollectorTimeoutWiring(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	target := config.TargetConfig{Name: "app", Type: config.TargetTypeCaddy, URL: server.URL, Host: "alpha.test"}
	if _, err := newCollector(target, 0); err == nil {
		t.Fatal("accepted zero timeout")
	}
	c, err := newCollector(target, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	r, err := c.Collect(context.Background())
	if err == nil || len(r.Samples) != 0 || time.Since(started) > time.Second {
		t.Fatalf("timeout: %+v %v", r, err)
	}
}
