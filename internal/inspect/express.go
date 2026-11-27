package inspect

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/prometheus"
	"github.com/pvrlabs/statlite/internal/urlshape"
)

func inspectExpress(ctx context.Context, endpoint string) (*Result, error) {
	u, err := urlshape.ParseHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("Express metrics URL: %w", err)
	}
	if u.User != nil || strings.TrimSpace(endpoint) != endpoint {
		return nil, errors.New("Express metrics URL must exclude user information and surrounding whitespace")
	}
	client, err := prometheus.NewClient(defaultTimeout, prometheus.DefaultLimits, nil)
	if err != nil {
		return nil, &Failure{Kind: FailureIncomplete, Err: err}
	}
	caps, warnings, err := collector.InspectExpress(ctx, endpoint, client)
	if err != nil {
		if errors.Is(err, collector.ErrExpressIncompatible) {
			return nil, &Failure{Kind: FailureIncompatible, Err: err}
		}
		return nil, prometheusInspectionFailure(err)
	}
	status := CompatibilityCompatible
	if len(warnings) != 0 {
		status = CompatibilityPartial
	}
	return &Result{TargetType: TargetExpress, Endpoint: endpoint, Capabilities: caps, Warnings: warnings, Status: status}, nil
}
