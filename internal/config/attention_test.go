package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAttentionConfig(t *testing.T) {
	cfg := validConfig(TargetTypeStatliteMetrics, "http://localhost/statlite/metrics")
	if !cfg.Attention.IsEnabled() || !cfg.Targets[0].Attention.HTTP5xx.IsEnabled() {
		t.Fatal("omission must enable Attention")
	}
	if rate, requests, errors := cfg.Targets[0].Attention.HTTP5xx.Thresholds(); rate != .10 || requests != 100 || errors != 10 {
		t.Fatalf("defaults: %v, %v, %v", rate, requests, errors)
	}

	data := `
server: {listen: "127.0.0.1:9090"}
storage: {sqlite_path: statlite.sqlite}
polling: {interval: 30s}
attention: {enabled: false}
targets:
  - name: app
    type: statlite-metrics
    url: http://localhost/statlite/metrics
    attention:
      http_5xx: {enabled: false, error_rate_percent: 2.5, min_requests: 50, min_errors: 3}
`
	for _, tc := range []struct{ old, replacement, wantError string }{
		{},
		{"2.5", "0", "error_rate_percent"},
		{"2.5", "101", "error_rate_percent"},
		{"2.5", ".nan", "error_rate_percent"},
		{"requests: 50", "requests: 0", "min_requests"},
		{"errors: 3", "errors: -1", "min_errors"},
	} {
		var decoded Config
		if err := yaml.Unmarshal([]byte(strings.Replace(data, tc.old, tc.replacement, 1)), &decoded); err != nil {
			t.Fatal(err)
		}
		err := Validate(&decoded)
		if tc.wantError != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("wanted %s, got %v", tc.wantError, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		settings := decoded.Targets[0].Attention.HTTP5xx
		rate, requests, errors := settings.Thresholds()
		if decoded.Attention.IsEnabled() || settings.IsEnabled() || rate != .025 || requests != 50 || errors != 3 {
			t.Fatalf("decoded settings: %+v rate %v min requests %d min errors %d", settings, rate, requests, errors)
		}
	}
}
