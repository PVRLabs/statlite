package config

import (
	"strings"
	"testing"
)

func TestLoadAcceptsGoExactMetricsURLAndBasicAuth(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: api
    type: go
    url: "https://example.com/metrics?scope=app"
    auth:
      type: basic
      username: user
      password: secret
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	target := cfg.Targets[0]
	if target.Type != TargetTypeGo || target.URL != "https://example.com/metrics?scope=app" {
		t.Fatalf("target = %#v, want literal Go metrics endpoint", target)
	}
	if got := target.DisplayMetadata(); got.Endpoint != target.URL || got.EndpointSource != "url" || got.Type != TargetTypeGo {
		t.Fatalf("DisplayMetadata() = %#v, want Go URL metadata", got)
	}
}

func TestLoadRejectsInvalidGoFields(t *testing.T) {
	tests := []struct{ name, fields, want string }{
		{"missing url", "", "url is required for type go"},
		{"actuator url", "url: http://example.com/metrics\n    actuator_base_url: http://example.com/actuator", "actuator_base_url is supported only for type spring"},
		{"empty actuator url", "url: http://example.com/metrics\n    actuator_base_url: \"\"", "actuator_base_url is supported only for type spring"},
		{"host metrics", "url: http://example.com/metrics\n    collect_host_metrics: true", "collect_host_metrics is supported only for type spring"},
		{"false host metrics", "url: http://example.com/metrics\n    collect_host_metrics: false", "collect_host_metrics is supported only for type spring"},
		{"spring source", "url: http://example.com/metrics\n    metrics_source: prometheus", "metrics_source is supported only for type spring"},
		{"empty spring source", "url: http://example.com/metrics\n    metrics_source: \"\"", "metrics_source is supported only for type spring"},
		{"health URL", "url: http://example.com/metrics\n    health_url: http://example.com/health", "health_url is supported only for type quarkus"},
		{"empty health URL", "url: http://example.com/metrics\n    health_url: \"\"", "health_url is supported only for type quarkus"},
		{"fragment", "url: http://example.com/metrics#section", "must not contain a fragment"},
		{"userinfo", "url: http://user:secret@example.com/metrics", "without user info"},
		{"scheme", "url: ftp://example.com/metrics", "must be an http or https URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: api
    type: go
    `+tt.fields+`
`)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want %q", err, tt.want)
			}
		})
	}
}
