package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pvrlabs/statlite/internal/urlshape"
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
	if !strings.Contains(err.Error(), "collect_host_metrics: is supported only for type spring") {
		t.Fatalf("Load() error = %q, want collect_host_metrics target type error", err)
	}
}

func TestLoadRejectsMisplacedSpringFieldsForStatliteMetrics(t *testing.T) {
	tests := []struct {
		name, fields, want string
		includeURL         bool
	}{
		{
			name:       "empty deprecated actuator URL",
			fields:     "\n    actuator_base_url: \"\"",
			want:       "actuator_base_url: is supported only for type spring",
			includeURL: true,
		},
		{
			name:       "empty metrics source",
			fields:     "\n    metrics_source: \"\"",
			want:       "metrics_source: is supported only for type spring",
			includeURL: true,
		},
		{
			name:       "explicit false host metrics",
			fields:     "\n    collect_host_metrics: false",
			want:       "collect_host_metrics: is supported only for type spring",
			includeURL: true,
		},
		{
			name:   "required URL error takes precedence",
			fields: "\n    actuator_base_url: http://example.com/actuator",
			want:   "url: is required for type statlite-metrics",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urlField := ""
			if tt.includeURL {
				urlField = "\n    url: http://example.com/statlite/metrics"
			}
			path := writeConfig(t, `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "30s"
targets:
  - name: "metrics"
    type: "statlite-metrics"`+urlField+tt.fields+`
`)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Load() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateRejectsProgrammaticUnsupportedTargetFields(t *testing.T) {
	tests := []struct {
		name, targetType, url, want string
		actuatorBaseURL, healthURL  string
	}{
		{
			name:            "StatLite Metrics actuator URL",
			targetType:      TargetTypeStatliteMetrics,
			url:             "http://example.com/statlite/metrics",
			actuatorBaseURL: "http://example.com/actuator",
			want:            "actuator_base_url: is supported only for type spring",
		},
		{
			name:            "Quarkus actuator URL",
			targetType:      TargetTypeQuarkus,
			url:             "http://example.com/q/metrics",
			actuatorBaseURL: "http://example.com/actuator",
			want:            "actuator_base_url: is supported only for type spring",
		},
		{
			name:       "Spring health URL",
			targetType: TargetTypeSpring,
			url:        "http://example.com/actuator",
			healthURL:  "http://example.com/health",
			want:       "health_url: is supported only for type quarkus",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Server:  ServerConfig{Listen: "127.0.0.1:9090"},
				Storage: StorageConfig{SQLitePath: "./statlite.sqlite"},
				Polling: PollingConfig{Interval: "30s"},
				Targets: []TargetConfig{{
					Type:            tt.targetType,
					Name:            "app",
					URL:             tt.url,
					ActuatorBaseURL: tt.actuatorBaseURL,
					HealthURL:       tt.healthURL,
				}},
			}
			if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateTargetURLsHaveSupportedStructure(t *testing.T) {
	types := []string{TargetTypeSpring, TargetTypeQuarkus, TargetTypeStatliteMetrics}
	invalidURLs := []struct{ value, reason string }{
		{"ftp://example.com/metrics", `unsupported URL scheme "ftp"`},
		{"http:///metrics", "must include a host"},
		{"http://user:pass@example.com/metrics", "embedded credentials"},
		{"http://example.com/metrics?x=1", "query string"},
		{"http://example.com/metrics#part", "fragment"},
		{"http://example.com/metrics#", "fragment"},
		{"http:example.com/metrics", "must include a host"},
		{"http://example.com:bad/metrics", "invalid URL"},
		{"http://example.com:0/metrics", "port must be a number from 1 through 65535"},
		{"http://example.com:99999/metrics", "port must be a number from 1 through 65535"},
	}
	for _, targetType := range types {
		for _, invalid := range invalidURLs {
			if strings.Contains(invalid.value, "?") && targetType != TargetTypeSpring {
				continue
			}
			t.Run(targetType+"/"+invalid.value, func(t *testing.T) {
				cfg := validConfig(targetType, invalid.value)
				err := Validate(cfg)
				if err == nil || !strings.Contains(err.Error(), `invalid target "orders": url:`) || !strings.Contains(err.Error(), invalid.reason) {
					t.Fatalf("Validate() error = %v, want named target URL error containing %q", err, invalid.reason)
				}
				var validation *TargetValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("Validate() error type = %T, want *TargetValidationError", err)
				}
				var shapeError *urlshape.Error
				sharedURLFailure := errors.As(err, &shapeError)
				configOnlyFailure := strings.Contains(invalid.reason, "embedded credentials") || strings.Contains(invalid.reason, "query string")
				if sharedURLFailure == configOnlyFailure {
					t.Fatalf("Validate() error chain = %T, shared URL classification = %v, want config-only classification %v", err, sharedURLFailure, configOnlyFailure)
				}
			})
		}
	}
}

func TestValidateStructurallyValidUnreachableURL(t *testing.T) {
	cfg := validConfig(TargetTypeSpring, "http://127.0.0.1:1/actuator")
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() error = %v, want structurally valid endpoint to pass without probing", err)
	}
}

func TestValidateAcceptsPercentEncodedHashInTargetURLs(t *testing.T) {
	for _, targetType := range []string{TargetTypeSpring, TargetTypeQuarkus, TargetTypeStatliteMetrics} {
		t.Run(targetType, func(t *testing.T) {
			cfg := validConfig(targetType, "http://example.com/metrics%23suffix")
			if err := Validate(cfg); err != nil {
				t.Fatalf("Validate() error = %v, want encoded # treated as path data", err)
			}
		})
	}
}

func TestValidatePollingDurationsMustBePositive(t *testing.T) {
	for _, field := range []string{"interval", "timeout"} {
		for _, value := range []string{"0s", "-1s"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				cfg := validConfig(TargetTypeSpring, "http://example.com/actuator")
				if field == "interval" {
					cfg.Polling.Interval = value
				} else {
					cfg.Polling.Timeout = value
				}
				err := Validate(cfg)
				if err == nil || !strings.Contains(err.Error(), "must be greater than zero") {
					t.Fatalf("Validate() error = %v, want positive duration error", err)
				}
			})
		}
	}
}

func TestValidateAcceptsUnreachableURLAndServiceNameListen(t *testing.T) {
	cfg := validConfig(TargetTypeQuarkus, "http://127.0.0.1:1/custom/metrics")
	cfg.Server.Listen = "localhost:http"
	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() error = %v, want nonblank listen value and unprobed endpoint", err)
	}
}

func validConfig(targetType, targetURL string) *Config {
	return &Config{
		Server:  ServerConfig{Listen: "127.0.0.1:9090"},
		Storage: StorageConfig{SQLitePath: "./statlite.sqlite"},
		Polling: PollingConfig{Interval: "30s", Timeout: "10s"},
		Targets: []TargetConfig{{Type: targetType, Name: "orders", URL: targetURL}},
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
	wantSQLitePath := filepath.Join(filepath.Dir(path), "from-env.sqlite")
	if cfg.Server.Listen != "127.0.0.1:9191" || cfg.Storage.SQLitePath != wantSQLitePath || cfg.Polling.Interval != "30s" {
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
	wantSQLitePath := filepath.Join(filepath.Dir(path), "${LITERAL_PATH}.sqlite")
	if cfg.Storage.SQLitePath != wantSQLitePath {
		t.Fatalf("Storage.SQLitePath = %q, want literal variable syntax", cfg.Storage.SQLitePath)
	}
}

func TestLoadResolvesRelativeSQLitePathFromConfigDirectory(t *testing.T) {
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve temporary directory: %v", err)
	}
	configDir := filepath.Join(root, "configs")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	configPath := filepath.Join(configDir, "statlite.yaml")
	content := `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "../data/./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "app"
    url: "http://example.com/actuator"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	previousWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("change working directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(previousWorkingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	relativeConfigPath := filepath.Join("configs", "..", "configs", "statlite.yaml")
	fromRelativePath, err := Load(relativeConfigPath)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", relativeConfigPath, err)
	}
	fromAbsolutePath, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load(%q) error = %v", configPath, err)
	}
	want := filepath.Join(root, "data", "statlite.sqlite")
	if fromRelativePath.Storage.SQLitePath != want {
		t.Fatalf("relative config SQLitePath = %q, want %q", fromRelativePath.Storage.SQLitePath, want)
	}
	if fromAbsolutePath.Storage.SQLitePath != want {
		t.Fatalf("absolute config SQLitePath = %q, want %q", fromAbsolutePath.Storage.SQLitePath, want)
	}
	if _, err := os.Stat(filepath.Join(root, "statlite.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("caller-directory database exists or cannot be checked: %v", err)
	}
}

func TestLoadResolvesRelativeSQLitePathFromConfigSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	realConfigDir := filepath.Join(root, "real")
	linkedConfigDir := filepath.Join(root, "linked")
	for _, dir := range []string{realConfigDir, linkedConfigDir} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("create directory: %v", err)
		}
	}
	realConfigPath := filepath.Join(realConfigDir, "statlite.yaml")
	linkedConfigPath := filepath.Join(linkedConfigDir, "statlite.yaml")
	content := `
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: "./statlite.sqlite"
polling:
  interval: "5m"
targets:
  - name: "app"
    url: "http://example.com/actuator"
`
	if err := os.WriteFile(realConfigPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Symlink(realConfigPath, linkedConfigPath); err != nil {
		t.Fatalf("symlink config: %v", err)
	}

	cfg, err := Load(linkedConfigPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := filepath.Join(linkedConfigDir, "statlite.sqlite")
	if cfg.Storage.SQLitePath != want {
		t.Fatalf("Storage.SQLitePath = %q, want symlink directory path %q", cfg.Storage.SQLitePath, want)
	}
}

func TestLoadLeavesAbsoluteSQLitePathUnchanged(t *testing.T) {
	absoluteSQLitePath := filepath.Join(t.TempDir(), "data", "..", "statlite.sqlite")
	path := writeConfig(t, strings.Replace(`
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: SQLITE_PATH
polling:
  interval: "5m"
targets:
  - name: "app"
    url: "http://example.com/actuator"
`, "SQLITE_PATH", absoluteSQLitePath, 1))

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Storage.SQLitePath != absoluteSQLitePath {
		t.Fatalf("Storage.SQLitePath = %q, want unchanged absolute path %q", cfg.Storage.SQLitePath, absoluteSQLitePath)
	}
}

func TestLoadWarnsAboutLegacyWorkingDirectorySQLitePath(t *testing.T) {
	tests := []struct {
		name         string
		newExists    bool
		legacyExists bool
		absolutePath bool
		sameDir      bool
		wantWarning  bool
	}{
		{name: "new database exists", newExists: true, legacyExists: true},
		{name: "legacy database exists", legacyExists: true, wantWarning: true},
		{name: "neither database exists"},
		{name: "absolute path", legacyExists: true, absolutePath: true},
		{name: "old and new paths are identical", newExists: true, legacyExists: true, sameDir: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			workingDir := filepath.Join(root, "work")
			configDir := filepath.Join(root, "config")
			if tt.sameDir {
				configDir = workingDir
			}
			for _, dir := range []string{workingDir, configDir} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatalf("create directory: %v", err)
				}
			}

			legacyPath := filepath.Join(workingDir, "history.sqlite")
			newPath := filepath.Join(configDir, "history.sqlite")
			if tt.legacyExists {
				if err := os.WriteFile(legacyPath, []byte("legacy"), 0o600); err != nil {
					t.Fatalf("write legacy database: %v", err)
				}
			}
			if tt.newExists && newPath != legacyPath {
				if err := os.WriteFile(newPath, []byte("new"), 0o600); err != nil {
					t.Fatalf("write new database: %v", err)
				}
			}

			sqlitePath := "./history.sqlite"
			if tt.absolutePath {
				sqlitePath = filepath.Join(root, "absolute.sqlite")
			}
			configPath := filepath.Join(configDir, "statlite.yaml")
			content := fmt.Sprintf(`
server:
  listen: "127.0.0.1:9090"
storage:
  sqlite_path: %q
polling:
  interval: "5m"
targets:
  - name: "app"
    url: "http://example.com/actuator"
`, sqlitePath)
			if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			previousWorkingDirectory, err := os.Getwd()
			if err != nil {
				t.Fatalf("get working directory: %v", err)
			}
			if err := os.Chdir(workingDir); err != nil {
				t.Fatalf("change working directory: %v", err)
			}
			t.Cleanup(func() {
				if err := os.Chdir(previousWorkingDirectory); err != nil {
					t.Errorf("restore working directory: %v", err)
				}
			})

			cfg, err := Load(configPath)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			warnings := cfg.DeprecationWarnings()
			if tt.wantWarning {
				if len(warnings) != 1 {
					t.Fatalf("DeprecationWarnings() = %#v, want one migration warning", warnings)
				}
				for _, want := range []string{"changed in StatLite v0.4.3", cfg.Storage.SQLitePath, "previous working-directory-relative path", legacyPath, "will continue"} {
					if !strings.Contains(warnings[0], want) {
						t.Errorf("warning = %q, missing %q", warnings[0], want)
					}
				}
				if _, err := os.Stat(cfg.Storage.SQLitePath); !os.IsNotExist(err) {
					t.Fatalf("compatibility check created new database or stat failed: %v", err)
				}
			} else if len(warnings) != 0 {
				t.Fatalf("DeprecationWarnings() = %#v, want none", warnings)
			}
		})
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

func TestDirectMetricsExampleConfigsLoad(t *testing.T) {
	tests := []struct {
		name string
		path string
		url  string
	}{
		{name: "FastAPI", path: "examples/python-fastapi-demo/statlite.yaml", url: "http://127.0.0.1:8000/statlite/metrics"},
		{name: "Express", path: "examples/node-express-demo/statlite.yaml", url: "http://127.0.0.1:3000/statlite/metrics"},
		{name: "Django", path: "examples/python-django-demo/statlite.yaml", url: "http://127.0.0.1:8000/statlite/metrics"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join("..", "..", tt.path)
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load(%q) error = %v", path, err)
			}
			if len(cfg.Targets) != 1 {
				t.Fatalf("Targets = %d, want 1", len(cfg.Targets))
			}
			target := cfg.Targets[0]
			if target.Type != TargetTypeStatliteMetrics || target.URL != tt.url {
				t.Fatalf("target = %#v, want %s statlite-metrics target", target, tt.name)
			}
		})
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
	if !strings.Contains(err.Error(), `type: unsupported value "json"`) {
		t.Fatalf("Load() error = %q, want unsupported target type", err)
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
			if !strings.Contains(err.Error(), "type: unsupported value") {
				t.Fatalf("Load() error = %q, want unsupported target type", err)
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
			if !strings.Contains(err.Error(), "auth: is supported only for type spring and quarkus") {
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
	if !strings.Contains(err.Error(), "auth.type: unsupported value") {
		t.Fatalf("Load() error = %q, want unsupported auth type", err)
	}
}

func TestValidateRejectsColonInBasicAuthUsername(t *testing.T) {
	cfg := validConfig(TargetTypeSpring, "http://example.com/actuator")
	cfg.Targets[0].Auth = &AuthConfig{Type: "basic", Username: "foo:bar", Password: "baz"}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "auth.username: must not contain ':'") {
		t.Fatalf("Validate() error = %v, want Basic Auth username delimiter error", err)
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
