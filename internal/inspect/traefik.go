package inspect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/prometheus"
	"github.com/pvrlabs/statlite/internal/urlshape"
)

// Traefik inspects an exact metrics endpoint for an explicitly selected service.
func Traefik(ctx context.Context, endpoint, service string) (*Result, error) {
	if !config.ValidTraefikService(service) {
		return nil, &Failure{Kind: FailureTypeUnavailable, Err: errors.New("typed Traefik inspection requires --service with an exact provider-qualified name such as alpha@file")}
	}
	u, err := urlshape.ParseHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Traefik metrics URL: %w", err)
	}
	if u.User != nil || strings.TrimSpace(endpoint) != endpoint {
		return nil, errors.New("Traefik metrics URL must exclude user information and surrounding whitespace")
	}
	client, err := prometheus.NewClient(defaultTimeout, prometheus.DefaultLimits, nil)
	if err != nil {
		return nil, &Failure{Kind: FailureIncomplete, Err: err}
	}
	caps, warnings, err := collector.InspectTraefik(ctx, endpoint, service, client)
	if err != nil {
		if errors.Is(err, collector.ErrTraefikIncompatible) {
			return nil, &Failure{Kind: FailureIncompatible, Err: err}
		}
		return nil, prometheusInspectionFailure(err)
	}
	status := CompatibilityCompatible
	if len(warnings) != 0 {
		status = CompatibilityPartial
	}
	return &Result{TargetType: TargetTraefik, Endpoint: endpoint, Service: service, Capabilities: caps, Warnings: warnings, Status: status}, nil
}
