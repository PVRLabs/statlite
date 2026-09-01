package app

// This file builds monitor managers and target collectors from loaded config.

import (
	"fmt"
	"net/url"
	"path"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/prometheus"
	"github.com/pvrlabs/statlite/internal/storage"
)

func NewMonitorManager(targets []config.TargetConfig, store *storage.Store, timeout, interval time.Duration) (*monitor.Manager, error) {
	managedTargets := make([]monitor.ManagedTarget, 0, len(targets))
	for _, target := range targets {
		targetCollector, err := newCollector(target, timeout)
		if err != nil {
			return nil, fmt.Errorf("%s: collector: %w", target.Name, err)
		}
		mon, err := monitor.New(target.Name, targetCollector, store, interval)
		if err != nil {
			return nil, fmt.Errorf("%s: monitor: %w", target.Name, err)
		}
		display := target.DisplayMetadata()
		managedTargets = append(managedTargets, monitor.ManagedTarget{
			Metadata: monitor.TargetMetadata{
				Name:           display.Name,
				Type:           display.Type,
				Endpoint:       display.Endpoint,
				EndpointSource: display.EndpointSource,
			},
			Monitor: mon,
		})
	}
	return monitor.NewManager(managedTargets)
}

func newCollector(target config.TargetConfig, timeout time.Duration) (monitor.Collector, error) {
	switch target.Type {
	case "", config.TargetTypeSpring:
		var auth *collector.BasicAuth
		if target.Auth != nil {
			auth = &collector.BasicAuth{
				Username: target.Auth.Username,
				Password: target.Auth.Password,
			}
		}
		actuatorClient, err := collector.NewActuatorClient(target.URL, timeout, auth)
		if err != nil {
			return nil, fmt.Errorf("actuator client: %w", err)
		}
		source := target.MetricsSource
		if source == "" {
			source = config.SpringMetricsSourceAuto
		}
		if target.UsesLegacyActuatorURLUserinfo() {
			source = config.SpringMetricsSourceActuator
		}
		if source == config.SpringMetricsSourceActuator {
			return collector.NewSpringCollector(target.Name, actuatorClient, nil, "", collector.SpringMetricsSource(source), target.CollectHostMetrics)
		}
		prometheusClient, err := prometheus.NewClient(timeout, prometheus.DefaultLimits, prometheusAuth(auth))
		if err != nil {
			return nil, fmt.Errorf("prometheus client: %w", err)
		}
		prometheusURL, err := springEndpoint(target.URL, "prometheus")
		if err != nil {
			return nil, fmt.Errorf("prometheus endpoint: %w", err)
		}
		return collector.NewSpringCollector(target.Name, actuatorClient, prometheusClient, prometheusURL, collector.SpringMetricsSource(source), target.CollectHostMetrics)
	case config.TargetTypeStatliteMetrics:
		client, err := collector.NewStatliteMetricsClient(target.URL, timeout)
		if err != nil {
			return nil, fmt.Errorf("statlite metrics client: %w", err)
		}
		return collector.NewStatliteMetricsCollector(target.Name, client), nil
	case config.TargetTypeQuarkus:
		client, err := prometheus.NewClient(timeout, prometheus.DefaultLimits, prometheusAuthConfig(target.Auth))
		if err != nil {
			return nil, fmt.Errorf("quarkus metrics client: %w", err)
		}
		return collector.NewQuarkusCollector(target.Name, target.URL, client), nil
	default:
		return nil, fmt.Errorf("unsupported target type %q", target.Type)
	}
}

func prometheusAuthConfig(auth *config.AuthConfig) *prometheus.BasicAuth {
	if auth == nil {
		return nil
	}
	return &prometheus.BasicAuth{Username: auth.Username, Password: auth.Password}
}

func prometheusAuth(auth *collector.BasicAuth) *prometheus.BasicAuth {
	if auth == nil {
		return nil
	}
	return &prometheus.BasicAuth{Username: auth.Username, Password: auth.Password}
}

func springEndpoint(base, endpoint string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = path.Join(u.Path, endpoint)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
