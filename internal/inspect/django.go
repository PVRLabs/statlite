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

// Django inspects an exact metrics endpoint for an explicitly selected django-prometheus recipe.
func Django(ctx context.Context, endpoint, mode string) (*Result, error) {
	if !config.ValidDjangoMetricsMode(mode) {
		return nil, &Failure{Kind: FailureTypeUnavailable, Err: errors.New("typed Django inspection requires --django-metrics-mode inline or separate")}
	}
	u, err := urlshape.ParseHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Django metrics URL: %w", err)
	}
	if u.User != nil || strings.TrimSpace(endpoint) != endpoint {
		return nil, errors.New("Django metrics URL must exclude user information and surrounding whitespace")
	}
	client, err := prometheus.NewClient(defaultTimeout, prometheus.DefaultLimits, nil)
	if err != nil {
		return nil, &Failure{Kind: FailureIncomplete, Err: err}
	}
	caps, warnings, err := collector.InspectDjango(ctx, endpoint, mode, client)
	if err != nil {
		if errors.Is(err, collector.ErrDjangoIncompatible) {
			return nil, &Failure{Kind: FailureIncompatible, Err: err}
		}
		return nil, prometheusInspectionFailure(err)
	}
	status := CompatibilityCompatible
	if len(warnings) != 0 {
		status = CompatibilityPartial
	}
	return &Result{TargetType: TargetDjango, Endpoint: endpoint, DjangoMetricsMode: mode, Capabilities: caps, Warnings: warnings, Status: status}, nil
}
