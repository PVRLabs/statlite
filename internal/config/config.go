package config

// This file loads and validates the statlite YAML configuration.

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pvrlabs/statlite/internal/urlshape"
	"gopkg.in/yaml.v3"
)

const (
	// When adding a target type, also update targetTypeHelp in the dashboard.
	TargetTypeSpring          = "spring"
	TargetTypeQuarkus         = "quarkus"
	TargetTypeStatliteMetrics = "statlite-metrics"

	SpringMetricsSourceAuto       = "auto"
	SpringMetricsSourcePrometheus = "prometheus"
	SpringMetricsSourceActuator   = "actuator"
)

type Config struct {
	Server              ServerConfig   `yaml:"server"`
	Storage             StorageConfig  `yaml:"storage"`
	Polling             PollingConfig  `yaml:"polling"`
	Targets             []TargetConfig `yaml:"targets"`
	deprecationWarnings []string
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type StorageConfig struct {
	SQLitePath    string `yaml:"sqlite_path"`
	RetentionDays int    `yaml:"retention_days"`
}

type PollingConfig struct {
	Interval string `yaml:"interval"`
	Timeout  string `yaml:"timeout"`
}

type TargetConfig struct {
	Type                   string      `yaml:"type,omitempty"`
	Name                   string      `yaml:"name"`
	ActuatorBaseURL        string      `yaml:"actuator_base_url"`
	URL                    string      `yaml:"url"`
	HealthURL              string      `yaml:"health_url,omitempty"`
	MetricsSource          string      `yaml:"metrics_source,omitempty"`
	CollectHostMetrics     bool        `yaml:"collect_host_metrics,omitempty"`
	Auth                   *AuthConfig `yaml:"auth,omitempty"`
	actuatorURLSet         bool
	metricsSourceSet       bool
	collectHostSet         bool
	healthURLSet           bool
	legacyActuatorUserinfo bool
	legacyStatliteTarget   bool
}

type TargetDisplayMetadata struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Endpoint       string `json:"endpoint"`
	EndpointSource string `json:"endpoint_source"`
}

type AuthConfig struct {
	Type     string `yaml:"type"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// TargetValidationError marks a target-scoped structural configuration error.
type TargetValidationError struct{ Err error }

func (e *TargetValidationError) Error() string { return e.Err.Error() }
func (e *TargetValidationError) Unwrap() error { return e.Err }

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	data = []byte(expandEnvironmentVariables(string(data)))

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(cfg.Storage.SQLitePath) {
		legacySQLitePath, err := filepath.Abs(cfg.Storage.SQLitePath)
		if err != nil {
			return nil, fmt.Errorf("resolving legacy SQLite path: %w", err)
		}
		configPath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolving config path: %w", err)
		}
		cfg.Storage.SQLitePath = filepath.Join(filepath.Dir(configPath), cfg.Storage.SQLitePath)
		cfg.warnIfLegacySQLiteDatabaseExists(legacySQLitePath)
	}

	return &cfg, nil
}

func (c *Config) warnIfLegacySQLiteDatabaseExists(legacyPath string) {
	if legacyPath == c.Storage.SQLitePath {
		return
	}
	if _, err := os.Stat(c.Storage.SQLitePath); !os.IsNotExist(err) {
		return
	}
	legacyInfo, err := os.Stat(legacyPath)
	if err != nil || legacyInfo.IsDir() {
		return
	}

	// TODO(compat): Remove the pre-v0.4.3 working-directory SQLite path check
	// after the migration window documented in docs/deprecations.md has passed.
	c.deprecationWarnings = append(c.deprecationWarnings, fmt.Sprintf(
		"storage.sqlite_path relative-path handling changed in StatLite v0.4.3. No database file exists at the new config-relative path %q, but an existing database file was found at the previous working-directory-relative path %q. StatLite will continue using %q. Existing history remains in %q. Move the database file or configure an absolute sqlite_path to keep using that history.",
		c.Storage.SQLitePath, legacyPath, c.Storage.SQLitePath, legacyPath,
	))
}

// Validate applies configuration defaults and checks that cfg is complete
// enough for normal StatLite startup. It is also used to validate generated
// onboarding snippets before they are shown to a user.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	for i := range cfg.Targets {
		target := &cfg.Targets[i]
		if target.Type == targetTypeStatliteLegacy && strings.Contains(target.URL, "#") {
			return targetURLValidationError(target, "url", fmt.Errorf("must not contain a fragment"))
		}
		if (target.Type == "" || target.Type == TargetTypeSpring) && target.URL != "" && target.ActuatorBaseURL != "" {
			return targetError(target, "url", "configures both url and deprecated actuator_base_url; use only url")
		}
	}
	cfg.deprecationWarnings = nil
	cfg.upgradeDeprecatedTargets()
	return cfg.validate()
}

func expandEnvironmentVariables(config string) string {
	// os.ExpandEnv treats "$$" as an environment variable named "$". Preserve
	// the documented "$${" escape sequence until after expansion instead.
	const literalVariablePrefix = "\x00statlite-literal-variable-prefix\x00"
	config = strings.ReplaceAll(config, "$${", literalVariablePrefix)
	return strings.ReplaceAll(os.ExpandEnv(config), literalVariablePrefix, "${")
}

func (s *StorageConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw struct {
		SQLitePath    string `yaml:"sqlite_path"`
		RetentionDays *int   `yaml:"retention_days"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}

	s.SQLitePath = raw.SQLitePath
	if raw.RetentionDays == nil {
		s.RetentionDays = 90
	} else {
		s.RetentionDays = *raw.RetentionDays
	}
	return nil
}

func (t *TargetConfig) UnmarshalYAML(value *yaml.Node) error {
	type plainTargetConfig TargetConfig
	var decoded plainTargetConfig
	if err := value.Decode(&decoded); err != nil {
		return err
	}
	*t = TargetConfig(decoded)
	for i := 0; i+1 < len(value.Content); i += 2 {
		switch value.Content[i].Value {
		case "actuator_base_url":
			t.actuatorURLSet = true
		case "metrics_source":
			t.metricsSourceSet = true
		case "collect_host_metrics":
			t.collectHostSet = true
		case "health_url":
			t.healthURLSet = true
		}
	}
	return nil
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Server.Listen) == "" {
		return fmt.Errorf("server.listen is required")
	}
	if c.Storage.SQLitePath == "" {
		return fmt.Errorf("storage.sqlite_path is required")
	}
	if c.Storage.RetentionDays < 0 {
		return fmt.Errorf("storage.retention_days must be greater than or equal to 0")
	}
	if c.Polling.Interval == "" {
		return fmt.Errorf("polling.interval is required")
	}
	interval, err := time.ParseDuration(c.Polling.Interval)
	if err != nil {
		return fmt.Errorf("polling.interval: invalid duration: %w", err)
	}
	if interval <= 0 {
		return fmt.Errorf("polling.interval: must be greater than zero")
	}
	if c.Polling.Timeout == "" {
		c.Polling.Timeout = "10s"
	}
	timeout, err := time.ParseDuration(c.Polling.Timeout)
	if err != nil {
		return fmt.Errorf("polling.timeout: invalid duration: %w", err)
	}
	if timeout <= 0 {
		return fmt.Errorf("polling.timeout: must be greater than zero")
	}
	return c.validateTargets()
}

func (c *Config) validateTargets() error {
	if len(c.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}
	seenTargetNames := make(map[string]int, len(c.Targets))
	for i := range c.Targets {
		target := &c.Targets[i]
		name := strings.TrimSpace(target.Name)
		if name == "" {
			return fmt.Errorf("targets[%d].name is required", i)
		}
		if previous, ok := seenTargetNames[name]; ok {
			return fmt.Errorf("targets[%d].name %q duplicates targets[%d].name", i, name, previous)
		}
		seenTargetNames[name] = i
		target.Name = name

		targetType := target.Type
		if targetType == "" {
			targetType = TargetTypeSpring
			target.Type = targetType
		}
		switch targetType {
		case TargetTypeSpring:
			if err := validateSpringTarget(target); err != nil {
				return err
			}
		case TargetTypeQuarkus:
			if err := validateQuarkusTarget(target); err != nil {
				return err
			}
		case TargetTypeStatliteMetrics:
			if err := validateStatliteMetricsTarget(target); err != nil {
				return err
			}
		default:
			return targetError(target, "type", fmt.Sprintf("unsupported value %q (supported: spring, quarkus, statlite-metrics)", targetType))
		}
		if target.Auth != nil {
			if targetType != TargetTypeSpring && targetType != TargetTypeQuarkus {
				return targetError(target, "auth", "is supported only for type spring and quarkus")
			}
			if target.Auth.Type != "basic" {
				return targetError(target, "auth.type", fmt.Sprintf("unsupported value %q (only basic is supported)", target.Auth.Type))
			}
			if target.Auth.Username == "" {
				return targetError(target, "auth.username", "is required when auth is configured")
			}
			if strings.Contains(target.Auth.Username, ":") {
				return targetError(target, "auth.username", "must not contain ':'")
			}
			if target.Auth.Password == "" {
				return targetError(target, "auth.password", "is required when auth is configured")
			}
		}
		if (target.CollectHostMetrics || target.collectHostSet) && targetType != TargetTypeSpring {
			return targetError(target, "collect_host_metrics", "is supported only for type spring")
		}
		if (target.HealthURL != "" || target.healthURLSet) && targetType != TargetTypeQuarkus {
			return targetError(target, "health_url", "is supported only for type quarkus")
		}
	}
	return nil
}

func validateSpringTarget(target *TargetConfig) error {
	if target.URL == "" {
		return targetError(target, "url", "is required for type spring")
	}
	if err := validateTargetURL(target.URL, !target.legacyActuatorUserinfo, !target.legacyActuatorUserinfo); err != nil {
		return targetURLValidationError(target, "url", err)
	}
	if target.MetricsSource == "" {
		target.MetricsSource = SpringMetricsSourceAuto
	} else if target.MetricsSource != SpringMetricsSourceAuto && target.MetricsSource != SpringMetricsSourcePrometheus && target.MetricsSource != SpringMetricsSourceActuator {
		return targetError(target, "metrics_source", fmt.Sprintf("unsupported value %q (supported: auto, prometheus, actuator)", target.MetricsSource))
	}
	if target.legacyActuatorUserinfo {
		if target.Auth != nil {
			return targetError(target, "auth", "cannot be combined with embedded credentials from deprecated actuator_base_url; use either the legacy URL credentials or url with explicit auth configuration")
		}
		if target.MetricsSource == SpringMetricsSourcePrometheus {
			return targetError(target, "metrics_source", "prometheus cannot be used with embedded credentials from deprecated actuator_base_url; use metrics_source: actuator or url with explicit auth configuration")
		}
		target.MetricsSource = SpringMetricsSourceActuator
	}
	return nil
}

func validateQuarkusTarget(target *TargetConfig) error {
	if target.URL == "" {
		return targetError(target, "url", "is required for type quarkus")
	}
	if target.ActuatorBaseURL != "" || target.actuatorURLSet {
		return targetError(target, "actuator_base_url", "is supported only for type spring")
	}
	if target.metricsSourceSet {
		return targetError(target, "metrics_source", "is supported only for type spring")
	}
	if target.collectHostSet {
		return targetError(target, "collect_host_metrics", "is supported only for type spring")
	}
	if err := validateTargetURL(target.URL, true, false); err != nil {
		return targetURLValidationError(target, "url", err)
	}
	if target.HealthURL != "" {
		if err := validateTargetURL(target.HealthURL, true, false); err != nil {
			return targetURLValidationError(target, "health_url", err)
		}
	}
	if target.MetricsSource != "" {
		return targetError(target, "metrics_source", "is supported only for type spring")
	}
	return nil
}

func validateStatliteMetricsTarget(target *TargetConfig) error {
	if target.URL == "" {
		return targetError(target, "url", fmt.Sprintf("is required for type %s", target.Type))
	}
	if err := validateTargetURL(target.URL, !target.legacyStatliteTarget, false); err != nil {
		return targetURLValidationError(target, "url", err)
	}
	if target.MetricsSource != "" {
		return targetError(target, "metrics_source", "is supported only for type spring")
	}
	if target.ActuatorBaseURL != "" || target.actuatorURLSet {
		return targetError(target, "actuator_base_url", "is supported only for type spring")
	}
	if target.metricsSourceSet {
		return targetError(target, "metrics_source", "is supported only for type spring")
	}
	return nil
}

func urlHasUserinfo(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.User != nil
}

// UsesLegacyActuatorURLUserinfo reports whether this Spring target came from
// the deprecated actuator_base_url compatibility path with embedded
// credentials. It is intentionally narrower than generic URL authentication.
func (t TargetConfig) UsesLegacyActuatorURLUserinfo() bool {
	return t.legacyActuatorUserinfo
}

func targetError(target *TargetConfig, field, reason string) error {
	return &TargetValidationError{
		Err: fmt.Errorf("invalid target %q: %s: %s", target.Name, field, reason),
	}
}

func targetURLValidationError(target *TargetConfig, field string, cause error) error {
	validation := &TargetValidationError{
		Err: fmt.Errorf("invalid target %q: %s: %w", target.Name, field, cause),
	}
	return validation
}

func validateTargetURL(raw string, rejectUserinfo, rejectQuery bool) error {
	if strings.TrimSpace(raw) != raw || raw == "" {
		return fmt.Errorf("must be an absolute URL without surrounding whitespace")
	}
	u, err := urlshape.ParseHTTP(raw)
	if err != nil {
		return err
	}
	if rejectUserinfo && u.User != nil {
		return fmt.Errorf("must not contain embedded credentials")
	}
	if rejectQuery && (u.RawQuery != "" || u.ForceQuery) {
		return fmt.Errorf("must not contain a query string")
	}
	return nil
}

// DefaultQuarkusHealthURL derives Quarkus's conventional aggregate SmallRye
// Health endpoint from its conventional metrics endpoint.
func DefaultQuarkusHealthURL(metricsURL string) (string, error) {
	u, err := url.Parse(metricsURL)
	if err != nil {
		return "", fmt.Errorf("parsing metrics URL: %w", err)
	}
	if !IsConventionalQuarkusMetricsPath(u.Path) {
		return "", nil
	}
	trimmedPath := strings.TrimRight(u.Path, "/")
	u.Path = strings.TrimSuffix(trimmedPath, "/q/metrics") + "/q/health"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// IsConventionalQuarkusMetricsPath reports whether a normalized URL path uses
// Quarkus's conventional metrics suffix, including an optional application
// context path and trailing slash.
func IsConventionalQuarkusMetricsPath(rawPath string) bool {
	return strings.HasSuffix(strings.TrimRight(rawPath, "/"), "/q/metrics")
}

func (t TargetConfig) DisplayMetadata() TargetDisplayMetadata {
	endpoint, source := t.displayEndpoint()
	return TargetDisplayMetadata{
		Name:           t.Name,
		Type:           t.Type,
		Endpoint:       sanitizeEndpoint(endpoint),
		EndpointSource: source,
	}
}

func (t TargetConfig) displayEndpoint() (string, string) {
	return t.URL, "url"
}

func sanitizeEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.User == nil {
		return endpoint
	}
	parsed.User = nil
	return parsed.String()
}
