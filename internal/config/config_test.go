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
	if target.Name != "from-env" || target.ActuatorBaseURL != "https://example.com/actuator" || target.Auth.Username != "admin" || target.Auth.Password != "secret" {
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

func TestLoadAcceptsStatliteTarget(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9091"
storage:
  sqlite_path: "./statlite-self.sqlite"
polling:
  interval: "30s"
  timeout: "5s"
targets:
  - name: "statlite-local"
    type: "statlite"
    url: "http://127.0.0.1:9090/healthz"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Targets[0].Type != "statlite" || cfg.Targets[0].URL == "" {
		t.Fatalf("target = %#v, want statlite URL target", cfg.Targets[0])
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
		EndpointSource: "actuator_base_url",
	}) {
		t.Fatalf("DisplayMetadata() = %#v, want sanitized spring endpoint", metadata)
	}
}

func TestTargetDisplayMetadataSanitizesStatliteEndpoint(t *testing.T) {
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
    url: "http://user:secret@127.0.0.1:9090/healthz"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	metadata := cfg.Targets[0].DisplayMetadata()
	if metadata != (TargetDisplayMetadata{
		Name:           "statlite-local",
		Type:           "statlite",
		Endpoint:       "http://127.0.0.1:9090/healthz",
		EndpointSource: "url",
	}) {
		t.Fatalf("DisplayMetadata() = %#v, want sanitized statlite endpoint", metadata)
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
			if cfg.Targets[0].Type != "statlite" {
				t.Fatalf("Targets[0].Type = %q, want statlite", cfg.Targets[0].Type)
			}
		})
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
    url: "http://example.com/healthz"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("Load() error = %q, want unsupported type", err)
	}
}

func TestLoadRejectsStatliteTargetWithoutURL(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "statlite"
    type: "statlite"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("Load() error = %q, want url required", err)
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
