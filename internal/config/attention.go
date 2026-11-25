package config

import "fmt"

// AttentionConfig controls the system as a whole. Omission enables it.
type AttentionConfig struct {
	Enabled          *bool    `yaml:"enabled,omitempty"`
	ErrorRatePercent *float64 `yaml:"error_rate_percent,omitempty"`
	MinRequests      *int     `yaml:"min_requests,omitempty"`
	MinErrors        *int     `yaml:"min_errors,omitempty"`
}

func (c AttentionConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

func (c AttentionConfig) Thresholds() (rate float64, requests, errors int) {
	rate, requests, errors = 0.10, 100, 10
	if c.ErrorRatePercent != nil {
		rate = *c.ErrorRatePercent / 100
	}
	if c.MinRequests != nil {
		requests = *c.MinRequests
	}
	if c.MinErrors != nil {
		errors = *c.MinErrors
	}
	return
}

func (c AttentionConfig) validate() error {
	rate, requests, errors := c.Thresholds()
	switch {
	case !(rate > 0 && rate <= 1):
		return fmt.Errorf("error_rate_percent must be greater than 0 and at most 100")
	case requests < 1:
		return fmt.Errorf("min_requests must be positive")
	case errors < 1:
		return fmt.Errorf("min_errors must be positive")
	}
	return nil
}
