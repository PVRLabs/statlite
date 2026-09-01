package config

import (
	"strings"
	"testing"
)

func TestLoadSpringURLAndMetricsSourceCombinations(t *testing.T) {
	for _, source := range []string{"", SpringMetricsSourceAuto, SpringMetricsSourcePrometheus, SpringMetricsSourceActuator} {
		t.Run("source="+source, func(t *testing.T) {
			metricsSource := ""
			if source != "" {
				metricsSource = "\n    metrics_source: " + source
			}
			path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"
    type: spring
    url: "https://example.com/actuator"`+metricsSource+`
`)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			want := source
			if want == "" {
				want = SpringMetricsSourceAuto
			}
			if cfg.Targets[0].MetricsSource != want {
				t.Fatalf("MetricsSource = %q, want %q", cfg.Targets[0].MetricsSource, want)
			}
			if len(cfg.DeprecationWarnings()) != 0 {
				t.Fatalf("DeprecationWarnings() = %v, want none", cfg.DeprecationWarnings())
			}
		})
	}
}

func TestLoadMigratesDeprecatedSpringActuatorBaseURL(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"
    actuator_base_url: "https://user:secret@example.com/actuator"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	target := cfg.Targets[0]
	if target.Type != TargetTypeSpring || target.URL != "https://user:secret@example.com/actuator" || target.ActuatorBaseURL != "" || target.MetricsSource != SpringMetricsSourceActuator || !target.UsesLegacyActuatorURLUserinfo() {
		t.Fatalf("target = %#v, want migrated Spring target", target)
	}
	warnings := cfg.DeprecationWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "actuator_base_url is deprecated; use url instead") || strings.Contains(warnings[0], "secret") {
		t.Fatalf("DeprecationWarnings() = %#v, want safe alias warning", warnings)
	}
}

func TestLoadLegacySpringActuatorUserinfoSourceRules(t *testing.T) {
	for _, source := range []string{"", SpringMetricsSourceAuto, SpringMetricsSourceActuator} {
		t.Run("source="+source, func(t *testing.T) {
			metricsSource := ""
			if source != "" {
				metricsSource = "\n    metrics_source: " + source
			}
			path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"
    actuator_base_url: "https://user:secret@example.com/actuator"`+metricsSource+`
`)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := cfg.Targets[0].MetricsSource; got != SpringMetricsSourceActuator {
				t.Fatalf("MetricsSource = %q, want actuator", got)
			}
		})
	}

	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"
    actuator_base_url: "https://user:secret@example.com/actuator"
    metrics_source: prometheus
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "prometheus cannot be used with embedded credentials from deprecated actuator_base_url") {
		t.Fatalf("Load() error = %v, want clear legacy credential source error", err)
	}
}

func TestLoadRejectsCanonicalSpringURLUserinfo(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"
    url: "https://user:secret@example.com/actuator"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "url must not contain embedded credentials; use the explicit auth configuration instead") {
		t.Fatalf("Load() error = %v, want actionable canonical URL credential error", err)
	}
}

func TestLoadRejectsLegacySpringURLUserinfoWithExplicitAuth(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"
    actuator_base_url: "https://legacy:secret@example.com/actuator"
    auth:
      type: basic
      username: current
      password: password
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "auth cannot be combined with embedded credentials from deprecated actuator_base_url") {
		t.Fatalf("Load() error = %v, want ambiguous credential configuration error", err)
	}
}

func TestLoadAcceptsCanonicalSpringURLWithExplicitAuth(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"
    url: "https://example.com/actuator"
    auth:
      type: basic
      username: user
      password: secret
`)
	if _, err := Load(path); err != nil {
		t.Fatalf("Load() error = %v, want explicit auth configuration accepted", err)
	}
}

func TestLoadDeprecatedSpringActuatorBaseURLWithoutUserinfoDefaultsAuto(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"
    actuator_base_url: "https://example.com/actuator"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Targets[0].MetricsSource != SpringMetricsSourceAuto || cfg.Targets[0].UsesLegacyActuatorURLUserinfo() {
		t.Fatalf("target = %#v, want normal auto alias behavior", cfg.Targets[0])
	}
}

func TestLoadRejectsBothSpringURLFields(t *testing.T) {
	for _, explicitType := range []string{"", "\n    type: spring"} {
		path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "app"`+explicitType+`
    url: "https://example.com/actuator"
    actuator_base_url: "https://example.com/actuator"
`)
		_, err := Load(path)
		if err == nil || !strings.Contains(err.Error(), "configures both url and deprecated actuator_base_url; use only url") {
			t.Fatalf("Load() error = %v, want ambiguous fields error", err)
		}
	}
}

func TestLoadRejectsInvalidOrMisplacedMetricsSource(t *testing.T) {
	tests := []struct {
		name, target, want string
	}{
		{"invalid Spring source", "type: spring\n    url: https://example.com/actuator\n    metrics_source: automatic", "supported: auto, prometheus, actuator"},
		{"non-Spring source", "type: statlite-metrics\n    url: https://example.com/statlite/metrics\n    metrics_source: actuator", "supported only for type spring"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, "server:\n  listen: 127.0.0.1:9090\nstorage:\n  sqlite_path: ./statlite.sqlite\npolling:\n  interval: 30s\ntargets:\n  - name: app\n    "+tt.target+"\n")
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want %q", err, tt.want)
			}
		})
	}
}
