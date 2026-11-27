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

func TestExpressInspect(t *testing.T) {
	const signature = "up 1\n"
	const httpMetrics = `http_request_duration_seconds_bucket{le="+Inf",status_code="404"} 1
http_request_duration_seconds_count{status_code="404"} 1
http_request_duration_seconds_sum{status_code="404"} 0.125
`
	const runtimeMetrics = `process_start_time_seconds 100
process_cpu_seconds_total 1
nodejs_heap_size_used_bytes 2048
`
	for _, tc := range []struct {
		name, body, want string
		exit             int
	}{
		{"cold", signature, "Compatibility: partial", 0},
		{"warm", signature + httpMetrics, "Compatibility: compatible", 0},
		{"configured", signature + httpMetrics + runtimeMetrics, "Compatibility: compatible", 0},
		{"other exporter", "runtime_gauge 1\n", "incompatible", 1},
		{"summary", signature + httpMetrics[strings.IndexByte(httpMetrics, '\n')+1:], "incompatible", 1},
		{"route labelled scrape", signature + strings.ReplaceAll(httpMetrics, `status_code="404"`, `route="/metrics",status_code="200"`), "incompatible", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0")
				fmt.Fprint(w, tc.body+"# EOF\n")
			}))
			defer server.Close()
			endpoint := server.URL + "/metrics"
			args := []string{"inspect", "--type", "express", "--name", "app", endpoint}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != tc.exit || !strings.Contains(out.String()+stderr.String(), tc.want) {
				t.Fatalf("exit=%d, stdout=%s, stderr=%s", code, &out, &stderr)
			}
			for _, capability := range []string{"requests", "http_404", "http_4xx", "http_5xx", "latency"} {
				if strings.Contains(out.String(), "✓ "+capability) != (tc.name == "warm" || tc.name == "configured") {
					t.Fatalf("unexpected %s capability: %s", capability, &out)
				}
			}
			for _, capability := range []string{"runtime_heap_used_bytes", "process_start_time", "process_cpu_usage"} {
				if strings.Contains(out.String(), "✓ "+capability) != (tc.name == "configured") {
					t.Fatalf("unexpected %s capability: %s", capability, &out)
				}
			}
			if tc.exit == 0 {
				path := filepath.Join(t.TempDir(), "statlite.yaml")
				if code := run(append(args, "--create-config", path), &out, &stderr); code != 0 {
					t.Fatalf("create-config exit=%d, stderr=%s", code, &stderr)
				}
				cfg, err := config.Load(path)
				if err != nil || len(cfg.Targets) != 1 || cfg.Targets[0].Type != config.TargetTypeExpress || cfg.Targets[0].URL != endpoint {
					t.Fatalf("config=%+v, error=%v", cfg, err)
				}
			}
		})
	}
}
