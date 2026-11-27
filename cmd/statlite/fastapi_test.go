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

func TestFastAPIInspectAndConfig(t *testing.T) {
	const signature = "http_request_duration_highr_seconds_count 2\nhttp_request_duration_highr_seconds_sum 0.2\n"
	for _, tc := range []struct {
		name, body, want string
		code             int
	}{
		{"cold", signature, "Compatibility: partial", 0},
		{"grouped", signature + "http_requests_total{handler=\"none\",method=\"GET\",status=\"4xx\"} 1\n", "exact 404 is unavailable", 0},
		{"overlapping HTTP family", "http_requests_total{handler=\"none\",method=\"GET\",status=\"404\"} 1\n", "incompatible", 1},
		{"exact", signature + `http_requests_total{handler="none",method="GET",status="404"} 1
http_request_duration_seconds_count{handler="none",method="GET"} 1
http_request_duration_seconds_sum{handler="none",method="GET"} 0.1
http_requests_total{handler="/custom",method="GET",status="2xx"} 1
`, "Compatibility: compatible", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/custom" {
					http.NotFound(w, r)
					return
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "statlite.yaml")
			var out, stderr bytes.Buffer
			args := []string{"inspect", "--type", "fastapi", server.URL + "/custom", "--name", "app"}
			status := run(args, &out, &stderr)
			if status != tc.code || !strings.Contains(out.String()+stderr.String(), tc.want) {
				t.Fatalf("status=%d, stdout=%s, stderr=%s", status, &out, &stderr)
			}
			if status == 0 {
				var createOut, createErr bytes.Buffer
				if status := run(append(args, "--create-config", path), &createOut, &createErr); status != 0 {
					t.Fatalf("create-config status=%d, stderr=%s", status, &createErr)
				}
				cfg, err := config.Load(path)
				if err != nil || len(cfg.Targets) != 1 || cfg.Targets[0].Type != config.TargetTypeFastAPI || cfg.Targets[0].URL != server.URL+"/custom" {
					t.Fatalf("config=%+v, err=%v", cfg, err)
				}
				if has404 := strings.Contains(out.String(), "✓ http_404"); has404 != (tc.name == "exact") {
					t.Fatalf("unexpected exact 404 capability: %s", &out)
				}
			}
		})
	}
}
