package config

// This file loads and validates the statlite YAML configuration.

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
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
	MetricsSource          string      `yaml:"metrics_source,omitempty"`
	CollectHostMetrics     bool        `yaml:"collect_host_metrics,omitempty"`
	Auth                   *AuthConfig `yaml:"auth,omitempty"`
	actuatorURLSet         bool
	metricsSourceSet       bool
	collectHostSet         bool
	legacyActuatorUserinfo bool
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

	return &cfg, nil
}

// Validate applies configuration defaults and checks that cfg is complete
// enough for normal StatLite startup. It is also used to validate generated
// onboarding snippets before they are shown to a user.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is required")
	}
	for i, target := range cfg.Targets {
		if (target.Type == "" || target.Type == TargetTypeSpring) && target.URL != "" && target.ActuatorBaseURL != "" {
			return fmt.Errorf("targets[%d] configures both url and deprecated actuator_base_url; use only url", i)
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
		}
	}
	return nil
}

func (c *Config) validate() error {
	if c.Server.Listen == "" {
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
	if _, err := time.ParseDuration(c.Polling.Interval); err != nil {
		return fmt.Errorf("polling.interval: invalid duration: %w", err)
	}
	if c.Polling.Timeout == "" {
		c.Polling.Timeout = "10s"
	}
	if _, err := time.ParseDuration(c.Polling.Timeout); err != nil {
		return fmt.Errorf("polling.timeout: invalid duration: %w", err)
	}
	if len(c.Targets) == 0 {
		return fmt.Errorf("at least one target is required")
	}
	seenTargetNames := make(map[string]int, len(c.Targets))
	for i, t := range c.Targets {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			return fmt.Errorf("targets[%d].name is required", i)
		}
		if previous, ok := seenTargetNames[name]; ok {
			return fmt.Errorf("targets[%d].name %q duplicates targets[%d].name", i, name, previous)
		}
		seenTargetNames[name] = i
		c.Targets[i].Name = name

		targetType := t.Type
		if targetType == "" {
			targetType = TargetTypeSpring
			c.Targets[i].Type = targetType
		}
		switch targetType {
		case TargetTypeSpring:
			if t.URL == "" {
				return fmt.Errorf("targets[%d].url is required for type spring", i)
			}
			if !t.legacyActuatorUserinfo && urlHasUserinfo(t.URL) {
				return fmt.Errorf("targets[%d].url must not contain embedded credentials; use the explicit auth configuration instead", i)
			}
			if t.MetricsSource == "" {
				c.Targets[i].MetricsSource = SpringMetricsSourceAuto
			} else if t.MetricsSource != SpringMetricsSourceAuto && t.MetricsSource != SpringMetricsSourcePrometheus && t.MetricsSource != SpringMetricsSourceActuator {
				return fmt.Errorf("targets[%d].metrics_source: unsupported value %q (supported: auto, prometheus, actuator)", i, t.MetricsSource)
			}
			if t.legacyActuatorUserinfo {
				if t.Auth != nil {
					return fmt.Errorf("targets[%d].auth cannot be combined with embedded credentials from deprecated actuator_base_url; use either the legacy URL credentials or url with explicit auth configuration", i)
				}
				if t.MetricsSource == SpringMetricsSourcePrometheus {
					return fmt.Errorf("targets[%d].metrics_source: prometheus cannot be used with embedded credentials from deprecated actuator_base_url; use metrics_source: actuator or url with explicit auth configuration", i)
				}
				c.Targets[i].MetricsSource = SpringMetricsSourceActuator
			}
		case TargetTypeQuarkus, TargetTypeStatliteMetrics:
			if t.URL == "" {
				return fmt.Errorf("targets[%d].url is required for type %s", i, targetType)
			}
			if targetType == TargetTypeQuarkus {
				if t.actuatorURLSet {
					return fmt.Errorf("targets[%d].actuator_base_url is supported only for type spring", i)
				}
				if t.metricsSourceSet {
					return fmt.Errorf("targets[%d].metrics_source is supported only for type spring", i)
				}
				if t.collectHostSet {
					return fmt.Errorf("targets[%d].collect_host_metrics is supported only for type spring", i)
				}
				if err := validateQuarkusURL(t.URL); err != nil {
					return fmt.Errorf("targets[%d].url for type quarkus: %w", i, err)
				}
			}
			if t.MetricsSource != "" {
				return fmt.Errorf("targets[%d].metrics_source is supported only for type spring", i)
			}
		default:
			return fmt.Errorf("targets[%d].type: unsupported type %q (supported: spring, quarkus, statlite-metrics)", i, targetType)
		}
		if t.Auth != nil {
			if targetType != TargetTypeSpring && targetType != TargetTypeQuarkus {
				return fmt.Errorf("targets[%d].auth is currently supported only for type spring and quarkus", i)
			}
			if t.Auth.Type != "basic" {
				return fmt.Errorf("targets[%d].auth.type: unsupported type %q (only 'basic' is supported)", i, t.Auth.Type)
			}
			if t.Auth.Username == "" {
				return fmt.Errorf("targets[%d].auth.username is required when auth is configured", i)
			}
			if t.Auth.Password == "" {
				return fmt.Errorf("targets[%d].auth.password is required when auth is configured", i)
			}
		}
		if t.CollectHostMetrics && targetType != TargetTypeSpring {
			return fmt.Errorf("targets[%d].collect_host_metrics is supported only for type spring", i)
		}
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

func validateQuarkusURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return fmt.Errorf("must be an http or https URL without user info")
	}
	if u.Fragment != "" {
		return fmt.Errorf("must not contain a fragment")
	}
	return nil
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
