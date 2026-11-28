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

func TestFastifyInspect(t *testing.T) {
	const runtime = "process_start_time_seconds 100\nprocess_cpu_seconds_total 1\nnodejs_heap_size_used_bytes 2048\n"
	const histogram = `http_request_duration_seconds_bucket{le="+Inf",route="__unknown__",method="GET",status_code="404"} 1
http_request_duration_seconds_count{method="GET",status_code="404",route="__unknown__"} 1
http_request_duration_seconds_sum{method="GET",route="__unknown__",status_code="404"} 0.125
`
	for _, tc := range []struct {
		name, body, want            string
		exit                        int
		http, runtime, scopeWarning bool
	}{
		{"cold", runtime, "Compatibility: partial", 0, false, true, true},
		{"scrape only", runtime + strings.ReplaceAll(histogram, "__unknown__", "/metrics"), "Compatibility: partial", 0, false, true, true},
		{"default scope", runtime + strings.ReplaceAll(histogram, "__unknown__", "/registered"), "Compatibility: partial", 0, true, true, true},
		{"configured", runtime + histogram, "Compatibility: compatible", 0, true, true, false},
		{"other exporter", "runtime_gauge 1\n", "incompatible", 1, false, false, false},
		{"summary", histogram[strings.IndexByte(histogram, '\n')+1:], "incompatible", 1, false, false, false},
		{"express", strings.NewReplacer(`route="__unknown__",`, "", `,route="__unknown__"`, "", `method="GET",`, "").Replace(histogram), "incompatible", 1, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			endpoint := server.URL + "/metrics"
			args := []string{"inspect", "--type", "fastify", "--name", "app", endpoint}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != tc.exit || !strings.Contains(out.String()+stderr.String(), tc.want) {
				t.Fatalf("exit=%d, stdout=%s, stderr=%s", code, &out, &stderr)
			}
			for _, capability := range []string{"requests", "http_404", "http_4xx", "http_5xx", "latency"} {
				if strings.Contains(out.String(), "✓ "+capability) != tc.http {
					t.Errorf("unexpected %s capability: %s", capability, &out)
				}
			}
			for _, capability := range []string{"runtime_heap_used_bytes", "process_start_time", "process_cpu_usage"} {
				if strings.Contains(out.String(), "✓ "+capability) != tc.runtime {
					t.Errorf("unexpected %s capability: %s", capability, &out)
				}
			}
			if strings.Contains(out.String(), "unmatched-route traffic has not been observed") != tc.scopeWarning {
				t.Errorf("unexpected scope warning: %s", &out)
			}
			if tc.exit == 0 {
				path := filepath.Join(t.TempDir(), "statlite.yaml")
				if code := run(append(args, "--create-config", path), &out, &stderr); code != 0 {
					t.Fatalf("create-config exit=%d, stderr=%s", code, &stderr)
				}
				cfg, err := config.Load(path)
				if err != nil || len(cfg.Targets) != 1 || cfg.Targets[0].Type != config.TargetTypeFastify || cfg.Targets[0].URL != endpoint {
					t.Fatalf("config=%+v, error=%v", cfg, err)
				}
			}
		})
	}
}
