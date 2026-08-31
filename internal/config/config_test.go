package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaultsPollingTimeout(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "app"
    actuator_base_url: "http://example.com/actuator"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Polling.Timeout != "10s" {
		t.Fatalf("Polling.Timeout = %q, want %q", cfg.Polling.Timeout, "10s")
	}
	if cfg.Storage.RetentionDays != 90 {
		t.Fatalf("Storage.RetentionDays = %d, want 90", cfg.Storage.RetentionDays)
	}
	if cfg.Targets[0].Type != "spring" {
		t.Fatalf("Targets[0].Type = %q, want spring", cfg.Targets[0].Type)
	}
	if cfg.Targets[0].CollectHostMetrics {
		t.Fatal("Targets[0].CollectHostMetrics = true, want false by default")
	}
}

func TestLoadAcceptsSpringHostMetricsOptIn(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "remote-app"
    actuator_base_url: "http://example.com/actuator"
    collect_host_metrics: true
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Targets[0].CollectHostMetrics {
		t.Fatal("Targets[0].CollectHostMetrics = false, want true")
	}
}

func TestLoadRejectsHostMetricsForNonSpringTarget(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "metrics"
    type: "statlite-metrics"
    url: "http://example.com/statlite/metrics"
    collect_host_metrics: true
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want collect_host_metrics target type error")
	}
	if !strings.Contains(err.Error(), "collect_host_metrics is supported only for type spring") {
		t.Fatalf("Load() error = %q, want collect_host_metrics target type error", err)
	}
}

func TestLoadExpandsEnvironmentVariablesAcrossConfig(t *testing.T) {
	t.Setenv("STATLITE_LISTEN", "127.0.0.1:9191")
	t.Setenv("STATLITE_DB_PATH", "./from-env.sqlite")
	t.Setenv("STATLITE_INTERVAL", "30s")
	t.Setenv("STATLITE_TARGET", "from-env")
	t.Setenv("STATLITE_ACTUATOR_URL", "https://example.com/actuator")
	t.Setenv("STATLITE_USERNAME", "admin")
	t.Setenv("STATLITE_PASSWORD", "secret")

	path := writeConfig(t, `
server:
  listen: "${STATLITE_LISTEN}"
storage:
  sqlite_path: "$STATLITE_DB_PATH"
polling:
  interval: "$STATLITE_INTERVAL"
targets:
  - name: "${STATLITE_TARGET}"
    actuator_base_url: "$STATLITE_ACTUATOR_URL"
    auth:
      type: "basic"
      username: "$STATLITE_USERNAME"
      password: "${STATLITE_PASSWORD}"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:9191" || cfg.Storage.SQLitePath != "./from-env.sqlite" || cfg.Polling.Interval != "30s" {
		t.Fatalf("expanded general config = %#v, want environment values", cfg)
	}
	target := cfg.Targets[0]
	if target.Name != "from-env" || target.URL != "https://example.com/actuator" || target.ActuatorBaseURL != "" || target.Auth.Username != "admin" || target.Auth.Password != "secret" {
		t.Fatalf("expanded target = %#v, want environment values", target)
	}
}

func TestLoadPreservesEscapedEnvironmentVariableSyntax(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./$${LITERAL_PATH}.sqlite"
polling:
  interval: "5m"
targets:
  - name: "app"
    actuator_base_url: "http://example.com/actuator"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Storage.SQLitePath != "./${LITERAL_PATH}.sqlite" {
		t.Fatalf("Storage.SQLitePath = %q, want literal variable syntax", cfg.Storage.SQLitePath)
	}
}

func TestLoadAcceptsStorageRetentionDays(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
  retention_days: 365
polling:
  interval: "5m"
targets:
  - name: "app"
    actuator_base_url: "http://example.com/actuator"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Storage.RetentionDays != 365 {
		t.Fatalf("Storage.RetentionDays = %d, want 365", cfg.Storage.RetentionDays)
	}
}

func TestLoadAcceptsUnlimitedStorageRetention(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
  retention_days: 0
polling:
  interval: "5m"
targets:
  - name: "app"
    actuator_base_url: "http://example.com/actuator"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Storage.RetentionDays != 0 {
		t.Fatalf("Storage.RetentionDays = %d, want 0", cfg.Storage.RetentionDays)
	}
}

func TestLoadRejectsNegativeStorageRetention(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
  retention_days: -1
polling:
  interval: "5m"
targets:
  - name: "app"
    actuator_base_url: "http://example.com/actuator"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want retention validation error")
	}
	if !strings.Contains(err.Error(), "storage.retention_days") {
		t.Fatalf("Load() error = %q, want storage.retention_days", err)
	}
}

func TestLoadAcceptsNonSpringTargetTypes(t *testing.T) {
	for _, targetType := range []string{TargetTypeStatliteMetrics} {
		t.Run(targetType, func(t *testing.T) {
			path := writeConfig(t, `
server:
  listen: "127.0.0.1:9091"
storage:
  sqlite_path: "./statlite-self.sqlite"
polling:
  interval: "30s"
targets:
  - name: "target"
    type: "`+targetType+`"
    url: "http://127.0.0.1:9090/statlite/metrics"
`)

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.Targets[0].Type != targetType {
				t.Fatalf("Targets[0].Type = %q, want %q", cfg.Targets[0].Type, targetType)
			}
		})
	}
}

func TestLoadRejectsDuplicateTargetNamesAfterTrimming(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "app"
    actuator_base_url: "http://example.com/actuator"
  - name: " app "
    actuator_base_url: "http://example.org/actuator"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want duplicate target error")
	}
	if !strings.Contains(err.Error(), `duplicates targets[0].name`) {
		t.Fatalf("Load() error = %q, want duplicate target name", err)
	}
}

func TestLoadTrimsTargetName(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: " app "
    actuator_base_url: "http://example.com/actuator"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Targets[0].Name != "app" {
		t.Fatalf("Targets[0].Name = %q, want app", cfg.Targets[0].Name)
	}
}

func TestTargetDisplayMetadataSanitizesSpringEndpoint(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "app"
    actuator_base_url: "http://user:secret@example.com:8080/actuator"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	metadata := cfg.Targets[0].DisplayMetadata()
	if metadata != (TargetDisplayMetadata{
		Name:           "app",
		Type:           "spring",
		Endpoint:       "http://example.com:8080/actuator",
		EndpointSource: "url",
	}) {
		t.Fatalf("DisplayMetadata() = %#v, want sanitized spring endpoint", metadata)
	}
}

func TestTargetDisplayMetadataUsesSanitizedURLForNonSpringTypes(t *testing.T) {
	for _, targetType := range []string{TargetTypeStatliteMetrics} {
		t.Run(targetType, func(t *testing.T) {
			target := TargetConfig{
				Name: "target",
				Type: targetType,
				URL:  "http://user:secret@example.com/statlite/metrics",
			}
			metadata := target.DisplayMetadata()
			if metadata.Endpoint != "http://example.com/statlite/metrics" || metadata.EndpointSource != "url" {
				t.Fatalf("DisplayMetadata() = %#v, want sanitized url endpoint", metadata)
			}
			if metadata.Type != targetType {
				t.Fatalf("DisplayMetadata().Type = %q, want configured type %q", metadata.Type, targetType)
			}
		})
	}
}

func TestStatliteExampleConfigsLoad(t *testing.T) {
	for _, name := range []string{"examples/statlite.yaml", "statlite.yaml"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", name)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load(%q) error = %v", path, err)
			}
			if cfg.Targets[0].Type != TargetTypeStatliteMetrics {
				t.Fatalf("Targets[0].Type = %q, want statlite-metrics", cfg.Targets[0].Type)
			}
		})
	}
}

func TestFastAPIExampleConfigLoads(t *testing.T) {
	path := filepath.Join("..", "..", "examples/python-fastapi-demo/statlite.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", path, err)
	}
	if len(cfg.Targets) != 1 {
		t.Fatalf("Targets = %d, want 1", len(cfg.Targets))
	}
	target := cfg.Targets[0]
	if target.Type != TargetTypeStatliteMetrics || target.URL != "http://127.0.0.1:8000/statlite/metrics" {
		t.Fatalf("target = %#v, want FastAPI statlite-metrics target", target)
	}
}

func TestMultiTargetExampleConfigLoads(t *testing.T) {
	path := filepath.Join("..", "..", "examples/multi-target.yaml")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", path, err)
	}
	if len(cfg.Targets) != 3 {
		t.Fatalf("Targets = %d, want 3", len(cfg.Targets))
	}
	wantTypes := []string{TargetTypeSpring, TargetTypeStatliteMetrics, TargetTypeStatliteMetrics}
	for i, want := range wantTypes {
		if cfg.Targets[i].Type != want {
			t.Fatalf("Targets[%d].Type = %q, want %q", i, cfg.Targets[i].Type, want)
		}
	}
}

func TestLoadRejectsUnknownTargetType(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "app"
    type: "json"
    url: "http://example.com/statlite/metrics"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("Load() error = %q, want unsupported type", err)
	}
	for _, targetType := range []string{"spring", "statlite-metrics"} {
		if !strings.Contains(err.Error(), targetType) {
			t.Fatalf("Load() error = %q, want supported type %q", err, targetType)
		}
	}
}

func TestLoadRejectsRetiredTargetTypes(t *testing.T) {
	for _, targetType := range []string{"statlite-health", "host"} {
		t.Run(targetType, func(t *testing.T) {
			path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "statlite"
    type: "`+targetType+`"
`)

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), "unsupported type") {
				t.Fatalf("Load() error = %q, want unsupported type", err)
			}
		})
	}
}

func TestLoadRejectsAuthForNonSpringTargets(t *testing.T) {
	for _, targetType := range []string{TargetTypeStatliteMetrics} {
		t.Run(targetType, func(t *testing.T) {
			path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "target"
    type: "`+targetType+`"
    url: "http://example.com/metrics"
    auth:
      type: "basic"
      username: "user"
      password: "secret"
`)

			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
			if !strings.Contains(err.Error(), "currently supported only for type spring") {
				t.Fatalf("Load() error = %q, want spring-only auth error", err)
			}
		})
	}
}

func TestLoadRejectsInvalidAuthType(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "app"
    actuator_base_url: "http://example.com/actuator"
    auth:
      type: "token"
      username: "u"
      password: "p"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("Load() error = %q, want unsupported type", err)
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}
