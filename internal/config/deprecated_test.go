package config

import (
	"strings"
	"testing"
)

func TestLoadMigratesLegacyStatliteTarget(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9091"
storage:
  sqlite_path: "./statlite-self.sqlite"
polling:
  interval: "30s"
targets:
  - name: "statlite-local"
    type: "statlite"
    url: "http://user:secret@127.0.0.1:9090/healthz?source=legacy"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	target := cfg.Targets[0]
	if target.Type != TargetTypeStatliteMetrics {
		t.Fatalf("Targets[0].Type = %q, want %q", target.Type, TargetTypeStatliteMetrics)
	}
	if target.URL != "http://user:secret@127.0.0.1:9090/statlite/metrics?source=legacy" {
		t.Fatalf("Targets[0].URL = %q, want migrated StatLite Metrics endpoint", target.URL)
	}
	warnings := cfg.DeprecationWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], `targets[0].type "statlite" is deprecated`) || !strings.Contains(warnings[0], "/statlite/metrics") {
		t.Fatalf("DeprecationWarnings() = %#v, want legacy target warning", warnings)
	}
	if strings.Contains(warnings[0], "secret") {
		t.Fatalf("DeprecationWarnings() = %#v, must not expose endpoint credentials", warnings)
	}
}

func TestLoadRejectsFragmentFromLegacyStatliteURL(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9091"
storage:
  sqlite_path: "./statlite-self.sqlite"
polling:
  interval: "30s"
targets:
  - name: "statlite-local"
    type: "statlite"
    url: "http://127.0.0.1:9090/healthz#"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "url: must not contain a fragment") {
		t.Fatalf("Load() error = %v, want legacy target fragment validation error", err)
	}
}
