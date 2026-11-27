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

func TestFlaskInspect(t *testing.T) {
	const signature = "flask_exporter_info{version=\"0.23.2\"} 1\n"
	const warm = `flask_http_request_total{method="GET",status="404"} 1
flask_http_request_duration_seconds_count{method="GET",status="404",path="/missing"} 1
flask_http_request_duration_seconds_sum{method="GET",status="404",path="/missing"} 0.125
process_start_time_seconds 100
process_cpu_seconds_total 1
`
	for _, tc := range []struct {
		name, body, want string
		exit             int
	}{
		{"cold", signature, "Compatibility: partial", 0},
		{"warm", signature + warm, "Compatibility: compatible", 0},
		{"overlapping families", warm, "incompatible", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/openmetrics-text; version=1.0.0")
				fmt.Fprint(w, tc.body+"# EOF\n")
			}))
			defer server.Close()
			endpoint := server.URL + "/metrics"
			args := []string{"inspect", "--type", "flask", "--name", "app", endpoint}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != tc.exit || !strings.Contains(out.String()+stderr.String(), tc.want) {
				t.Fatalf("exit=%d, stdout=%s, stderr=%s", code, &out, &stderr)
			}
			for _, capability := range []string{"requests", "http_404", "http_4xx", "http_5xx", "latency", "process_start_time", "process_cpu_usage"} {
				if strings.Contains(out.String(), "✓ "+capability) != (tc.name == "warm") {
					t.Fatalf("unexpected %s capability: %s", capability, &out)
				}
			}
			if tc.exit == 0 {
				path := filepath.Join(t.TempDir(), "statlite.yaml")
				if code := run(append(args, "--create-config", path), &out, &stderr); code != 0 {
					t.Fatalf("create-config exit=%d, stderr=%s", code, &stderr)
				}
				cfg, err := config.Load(path)
				if err != nil || len(cfg.Targets) != 1 || cfg.Targets[0].Type != config.TargetTypeFlask || cfg.Targets[0].URL != endpoint {
					t.Fatalf("config=%+v, error=%v", cfg, err)
				}
			}
		})
	}
}
