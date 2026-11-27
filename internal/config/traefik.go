package config

import "regexp"

// Traefik qualifies the configured name verbatim using @ as the separator.
var traefikServicePattern = regexp.MustCompile(`^[^@\p{Cc}]+@[a-zA-Z0-9_-]+$`)

func ValidTraefikService(service string) bool {
	return len(service) <= 1024 && traefikServicePattern.MatchString(service)
}

func validateTraefikTarget(target *TargetConfig) error {
	if target.URL == "" {
		return targetError(target, "url", "is required for type traefik")
	}
	if err := validateTargetURL(target.URL, true, false); err != nil {
		return targetURLValidationError(target, "url", err)
	}
	if target.ActuatorBaseURL != "" || target.actuatorURLSet {
		return targetError(target, "actuator_base_url", "is supported only for type spring")
	}
	if target.MetricsSource != "" || target.metricsSourceSet {
		return targetError(target, "metrics_source", "is supported only for type spring")
	}
	if !ValidTraefikService(target.Service) {
		return targetError(target, "service", "must be an exact provider-qualified name such as alpha@file")
	}
	return nil
}
