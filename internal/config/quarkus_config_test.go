package config

import (
	"strings"
	"testing"
)

func TestLoadAcceptsQuarkusExactMetricsURLAndBasicAuth(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: orders
    type: quarkus
    url: "http://localhost:9000/q/metrics/?scope=app"
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
	if target.Type != TargetTypeQuarkus || target.URL != "http://localhost:9000/q/metrics/?scope=app" {
		t.Fatalf("target = %#v, want literal Quarkus endpoint", target)
	}
	if got := target.DisplayMetadata(); got.Endpoint != target.URL || got.EndpointSource != "url" || got.Type != TargetTypeQuarkus {
		t.Fatalf("DisplayMetadata() = %#v, want Quarkus URL metadata", got)
	}
}

func TestLoadRejectsInvalidQuarkusFields(t *testing.T) {
	tests := []struct{ name, fields, want string }{
		{"missing url", "", "url is required for type quarkus"},
		{"actuator url", "url: http://example.com/q/metrics\n    actuator_base_url: http://example.com/actuator", "actuator_base_url is supported only for type spring"},
		{"host metrics", "url: http://example.com/q/metrics\n    collect_host_metrics: true", "collect_host_metrics is supported only for type spring"},
		{"false host metrics", "url: http://example.com/q/metrics\n    collect_host_metrics: false", "collect_host_metrics is supported only for type spring"},
		{"spring source", "url: http://example.com/q/metrics\n    metrics_source: prometheus", "metrics_source is supported only for type spring"},
		{"empty spring source", "url: http://example.com/q/metrics\n    metrics_source: \"\"", "metrics_source is supported only for type spring"},
		{"empty actuator url", "url: http://example.com/q/metrics\n    actuator_base_url: \"\"", "actuator_base_url is supported only for type spring"},
		{"fragment", "url: http://example.com/q/metrics#section", "must not contain a fragment"},
		{"userinfo", "url: http://user:secret@example.com/q/metrics", "without user info"},
		{"scheme", "url: ftp://example.com/q/metrics", "must be an http or https URL"},
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
  - name: orders
    type: quarkus
    `+tt.fields+`
`)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want %q", err, tt.want)
			}
		})
	}
}
