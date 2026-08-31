package config

// This file contains temporary configuration migrations for retired formats.

import (
	"fmt"
	"net/url"
	"strings"
)

const targetTypeStatliteLegacy = "statlite"

// TODO: Remove this compatibility upgrade in a future major version.
func (c *Config) upgradeDeprecatedTargets() {
	for i := range c.Targets {
		target := &c.Targets[i]
		if target.Type == "" || target.Type == TargetTypeSpring {
			if target.URL != "" && target.ActuatorBaseURL != "" {
				continue
			}
			if target.ActuatorBaseURL != "" {
				target.URL = target.ActuatorBaseURL
				target.ActuatorBaseURL = ""
				c.deprecationWarnings = append(c.deprecationWarnings, fmt.Sprintf("targets[%d].actuator_base_url is deprecated; use url instead", i))
			}
		}
		if target.Type != targetTypeStatliteLegacy {
			continue
		}

		target.Type = TargetTypeStatliteMetrics
		target.URL = statliteMetricsURL(target.URL)
		// Do not include the endpoint: it may contain credentials.
		c.deprecationWarnings = append(c.deprecationWarnings, fmt.Sprintf("targets[%d].type %q is deprecated; using type %q with /statlite/metrics", i, targetTypeStatliteLegacy, TargetTypeStatliteMetrics))
	}
}

// DeprecationWarnings reports configuration migrations that should be shown at
// startup. It intentionally omits endpoints so credentials can never reach logs.
func (c *Config) DeprecationWarnings() []string {
	return append([]string(nil), c.deprecationWarnings...)
}

func statliteMetricsURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	parsed.Path = "/statlite/metrics"
	parsed.RawPath = ""
	return parsed.String()
}
