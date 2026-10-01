package config

import (
	"strings"
	"testing"
)

func TestLoadAcceptsMicronautExactMetricsURLAndBasicAuth(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: orders
    type: micronaut
    url: "http://localhost:9000/prometheus/?scope=app"
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
	if target.Type != TargetTypeMicronaut || target.URL != "http://localhost:9000/prometheus/?scope=app" {
		t.Fatalf("target = %#v, want literal Micronaut endpoint", target)
	}
	healthURL, err := DefaultMicronautHealthURL(target.URL)
	if err != nil || healthURL != "http://localhost:9000/health" {
		t.Fatalf("DefaultMicronautHealthURL() = %q, %v; want conventional Micronaut health endpoint", healthURL, err)
	}
	if got := target.DisplayMetadata(); got.Endpoint != target.URL || got.EndpointSource != "url" || got.Type != TargetTypeMicronaut {
		t.Fatalf("DisplayMetadata() = %#v, want Micronaut URL metadata", got)
	}
}

func TestLoadRejectsInvalidMicronautFields(t *testing.T) {
	tests := []struct{ name, fields, want string }{
		{"missing url", "", "url: is required for type micronaut"},
		{"actuator url", "url: http://example.com/prometheus\n    actuator_base_url: http://example.com/actuator", "actuator_base_url: is supported only for type spring"},
		{"host metrics", "url: http://example.com/prometheus\n    collect_host_metrics: true", "collect_host_metrics: is supported only for type spring"},
		{"false host metrics", "url: http://example.com/prometheus\n    collect_host_metrics: false", "collect_host_metrics: is supported only for type spring"},
		{"spring source", "url: http://example.com/prometheus\n    metrics_source: prometheus", "metrics_source: is supported only for type spring"},
		{"empty spring source", "url: http://example.com/prometheus\n    metrics_source: \"\"", "metrics_source: is supported only for type spring"},
		{"empty actuator url", "url: http://example.com/prometheus\n    actuator_base_url: \"\"", "actuator_base_url: is supported only for type spring"},
		{"whitespace", "url: \"http://example.com/prometheus \"", "without surrounding whitespace"},
		{"fragment", "url: http://example.com/prometheus#section", "must not contain a fragment"},
		{"userinfo", "url: http://user:secret@example.com/prometheus", "url: must not contain embedded credentials"},
		{"scheme", "url: ftp://example.com/prometheus", `url: unsupported URL scheme "ftp"`},
		{"health fragment", "url: http://example.com/prometheus\n    health_url: http://example.com/health#section", "health_url: must not contain a fragment"},
		{"health empty fragment", "url: http://example.com/prometheus\n    health_url: http://example.com/health#", "health_url: must not contain a fragment"},
		{"health userinfo", "url: http://example.com/prometheus\n    health_url: http://user:secret@example.com/health", "health_url: must not contain embedded credentials"},
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
    type: micronaut
    `+tt.fields+`
`)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadAcceptsExplicitMicronautHealthURL(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: orders
    type: micronaut
    url: "http://example.com/manage/prom"
    health_url: "https://health.example.com/manage%2Fhealth/?probe=1"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.Targets[0].HealthURL; got != "https://health.example.com/manage%2Fhealth/?probe=1" {
		t.Fatalf("HealthURL = %q, want explicit override", got)
	}
}

func TestLoadAcceptsCustomMicronautMetricsEndpointWithoutDerivedHealth(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: orders
    type: micronaut
    url: "http://example.com/manage/prom"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got, err := DefaultMicronautHealthURL(cfg.Targets[0].URL)
	if err != nil || got != "" {
		t.Fatalf("DefaultMicronautHealthURL() = %q, %v; want unavailable convention", got, err)
	}
}

func TestDefaultMicronautHealthURLPreservesContextAndDropsMetricsQuery(t *testing.T) {
	tests := []struct {
		metrics string
		want    string
	}{
		{"http://localhost:9000/prometheus", "http://localhost:9000/health"},
		{"http://localhost:9000/prometheus/", "http://localhost:9000/health"},
		{"https://example.com/service/prometheus?scope=app", "https://example.com/service/health"},
		{"http://example.com/manage/metrics", ""},
		{"http://example.com/svc%2Fwest/prometheus/?", "http://example.com/svc%2Fwest/health"},
		{"http://example.com/svc/%70rometheus", ""},
		{"http://example.com/prometheus//", ""},
		{"http://example.com/a//../prometheus", "http://example.com/a//../health"},
		{"http://example.com/foo/metrics", ""},
		{"http://example.com/prometheus-extra", ""},
	}
	for _, tt := range tests {
		got, err := DefaultMicronautHealthURL(tt.metrics)
		if err != nil {
			t.Fatalf("DefaultMicronautHealthURL(%q) error = %v", tt.metrics, err)
		}
		if got != tt.want {
			t.Fatalf("DefaultMicronautHealthURL(%q) = %q, want %q", tt.metrics, got, tt.want)
		}
	}
}

func TestMicronautAuthValidation(t *testing.T) {
	for _, auth := range []*AuthConfig{
		{Type: "bearer", Username: "u", Password: "p"},
		{Type: "basic", Password: "p"},
		{Type: "basic", Username: "u"},
		{Type: "basic", Username: "u:x", Password: "p"},
	} {
		cfg := validConfig(TargetTypeMicronaut, "http://example.com/prometheus")
		cfg.Targets[0].Auth = auth
		if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "auth") {
			t.Fatalf("invalid auth accepted: %v", err)
		}
	}
}
