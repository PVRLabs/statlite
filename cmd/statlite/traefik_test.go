package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pvrlabs/statlite/internal/config"
)

func TestTraefikInspectAndConfig(t *testing.T) {
	const labels = `{service="alpha@file",code="200",method="GET",protocol="http"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, "traefik_config_reloads_total 1")
		fmt.Fprintln(w, "traefik_service_requests_total"+labels+" 1")
		fmt.Fprintln(w, "traefik_service_request_duration_seconds_count"+labels+" 1")
		fmt.Fprintln(w, "traefik_service_request_duration_seconds_sum"+labels+" 0.1")
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "statlite.yaml")
	var out, stderr bytes.Buffer
	args := []string{"inspect", server.URL + "/custom", "--type", "traefik"}
	runTraefik := func(extra ...string) int {
		t.Helper()
		out.Reset()
		stderr.Reset()
		return run(append(args, extra...), &out, &stderr)
	}
	if status := runTraefik("--service", "alpha"); status != 2 || !strings.Contains(stderr.String(), "provider-qualified") {
		t.Fatalf("bad selector: status=%d, stderr=%s", status, &stderr)
	}
	if status := runTraefik("--service", "alpha@file"); status != 0 || !strings.Contains(out.String(), "latency") ||
		!strings.Contains(out.String(), "url: "+server.URL+"/custom\n      service: alpha@file") || !strings.Contains(out.String(), "--service 'alpha@file'") {
		t.Fatalf("warm inspection: status=%d, out=%s, stderr=%s", status, &out, &stderr)
	}
	if status := runTraefik("--service", "alpha@file", "--name", "alpha", "--create-config", path); status != 0 {
		t.Fatalf("create-config: status=%d, stderr=%s", status, &stderr)
	}
	if status := runTraefik("--service", "beta@file"); status != 0 || !strings.Contains(out.String(), "Compatibility: partial") ||
		!strings.Contains(out.String(), `HTTP request metrics not observed for service "beta@file"`) ||
		strings.Contains(out.String(), "latency") {
		t.Fatalf("cold inspection: status=%d, out=%s, stderr=%s", status, &out, &stderr)
	}
	if status := runTraefik("--service", "beta@file", "--name", "beta", "--add-to-config", path); status != 0 {
		t.Fatalf("add-to-config: status=%d, stderr=%s", status, &stderr)
	}
	if status := runTraefik("--service", "beta@file", "--name", "other", "--add-to-config", path); status == 0 {
		t.Fatalf("duplicate selector was accepted: %s", &out)
	}
	cfg, err := config.Load(path)
	if err != nil || len(cfg.Targets) != 2 || cfg.Targets[0].Service != "alpha@file" || cfg.Targets[1].Service != "beta@file" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
}

func TestTraefikInspectRejectsUnrelatedPrometheusEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "process_start_time_seconds 123\nhttp_requests_total 4")
	}))
	defer server.Close()
	var out, stderr bytes.Buffer
	status := run([]string{"inspect", "--type", "traefik", "--service", "alpha@file", server.URL}, &out, &stderr)
	if status == 0 || !strings.Contains(stderr.String(), "incompatible") {
		t.Fatalf("unrelated endpoint: status=%d, out=%s, stderr=%s", status, &out, &stderr)
	}
}
