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

type Config struct {
	Server  ServerConfig   `yaml:"server"`
	Storage StorageConfig  `yaml:"storage"`
	Polling PollingConfig  `yaml:"polling"`
	Targets []TargetConfig `yaml:"targets"`
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
	Type            string      `yaml:"type,omitempty"`
	Name            string      `yaml:"name"`
	ActuatorBaseURL string      `yaml:"actuator_base_url"`
	URL             string      `yaml:"url"`
	Auth            *AuthConfig `yaml:"auth,omitempty"`
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

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
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
			targetType = "spring"
			c.Targets[i].Type = targetType
		}
		switch targetType {
		case "spring":
			if t.ActuatorBaseURL == "" {
				return fmt.Errorf("targets[%d].actuator_base_url is required", i)
			}
		case "statlite":
			if t.URL == "" {
				return fmt.Errorf("targets[%d].url is required for type statlite", i)
			}
		default:
			return fmt.Errorf("targets[%d].type: unsupported type %q (supported: spring, statlite)", i, targetType)
		}
		if t.Auth != nil {
			if targetType != "spring" {
				return fmt.Errorf("targets[%d].auth is only supported for type spring", i)
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
	switch t.Type {
	case "statlite":
		return t.URL, "url"
	default:
		return t.ActuatorBaseURL, "actuator_base_url"
	}
}

func sanitizeEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.User == nil {
		return endpoint
	}
	parsed.User = nil
	return parsed.String()
}
