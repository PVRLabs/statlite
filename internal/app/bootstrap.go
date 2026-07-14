package app

// This file builds monitor managers and target collectors from loaded config.

import (
	"fmt"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/monitor"
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
	case "", "spring":
		var auth *collector.BasicAuth
		if target.Auth != nil {
			auth = &collector.BasicAuth{
				Username: target.Auth.Username,
				Password: target.Auth.Password,
			}
		}
		actuatorClient, err := collector.NewActuatorClient(target.ActuatorBaseURL, timeout, auth)
		if err != nil {
			return nil, fmt.Errorf("actuator client: %w", err)
		}
		return collector.NewSpringActuatorCollector(target.Name, actuatorClient), nil
	case "statlite":
		client, err := collector.NewStatliteHealthzClient(target.URL, timeout)
		if err != nil {
			return nil, fmt.Errorf("statlite healthz client: %w", err)
		}
		return collector.NewStatliteHealthzCollector(target.Name, client), nil
	default:
		return nil, fmt.Errorf("unsupported target type %q", target.Type)
	}
}
