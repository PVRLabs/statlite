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

func TestDjangoInspect(t *testing.T) {
	const signature = "django_http_requests_before_middlewares_total 2\n"
	const inline = `django_http_responses_total_by_status_view_method_total{status="404",view="<unnamed view>",method="GET"} 1
django_http_responses_total_by_status_view_method_total{status="200",view="prometheus-django-metrics",method="GET"} 1
`
	const separate = `django_http_responses_total_by_status_total{status="404"} 1
django_http_requests_latency_including_middlewares_seconds_count 1
django_http_requests_latency_including_middlewares_seconds_sum 0.1
`
	for _, tc := range []struct {
		name, targetType, mode, body, want string
		code                               int
		latency                            bool
	}{
		{"missing mode", "django", "", signature, "requires --django-metrics-mode", 2, false},
		{"invalid mode", "django", "auto", signature, "requires --django-metrics-mode", 2, false},
		{"wrong type", "fastapi", "inline", signature, "requires --type django", 2, false},
		{"cold inline", "django", "inline", signature, "Compatibility: partial", 0, false},
		{"cold separate", "django", "separate", signature, "Compatibility: partial", 0, false},
		{"inline", "django", "inline", signature + inline, "Compatibility: compatible", 0, false},
		{"separate", "django", "separate", signature + separate, "Compatibility: compatible", 0, true},
		{"contradictory view", "django", "separate", signature + inline + separate, "observed the inline Django metrics view", 1, false},
		{"unrelated", "django", "inline", "process_start_time_seconds 1\n", "incompatible", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/plain; version=1.0.0")
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			args := []string{"inspect", "--type", tc.targetType, server.URL, "--name", "app"}
			if tc.mode != "" {
				args = append(args, "--django-metrics-mode", tc.mode)
			}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != tc.code || !strings.Contains(out.String()+stderr.String(), tc.want) {
				t.Fatalf("status=%d, stdout=%s, stderr=%s", code, &out, &stderr)
			}
			if strings.Contains(out.String(), "✓ latency") != tc.latency {
				t.Fatalf("unexpected latency capability: %s", &out)
			}
			if tc.code == 0 {
				path := filepath.Join(t.TempDir(), "statlite.yaml")
				if code := run(append(args, "--create-config", path), &out, &stderr); code != 0 {
					t.Fatalf("create-config status=%d, stderr=%s", code, &stderr)
				}
				cfg, err := config.Load(path)
				if err != nil || len(cfg.Targets) != 1 || cfg.Targets[0].DjangoMetricsMode != tc.mode {
					t.Fatalf("config=%+v, err=%v", cfg, err)
				}
			}
		})
	}
}
