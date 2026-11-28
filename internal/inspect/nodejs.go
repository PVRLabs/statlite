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
	return inspectNodeExporter(ctx, endpoint, "Express", TargetExpress, collector.InspectExpress, collector.ErrExpressIncompatible)
}

func inspectFastify(ctx context.Context, endpoint string) (*Result, error) {
	return inspectNodeExporter(ctx, endpoint, "Fastify", TargetFastify, collector.InspectFastify, collector.ErrFastifyIncompatible)
}

func inspectNodeExporter(ctx context.Context, endpoint, name string, targetType TargetType,
	inspect func(context.Context, string, *prometheus.Client) ([]string, []string, error), incompatible error,
) (*Result, error) {
	u, err := urlshape.ParseHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("%s metrics URL: %w", name, err)
	}
	if u.User != nil || strings.TrimSpace(endpoint) != endpoint {
		return nil, fmt.Errorf("%s metrics URL must exclude user information and surrounding whitespace", name)
	}
	client, err := prometheus.NewClient(defaultTimeout, prometheus.DefaultLimits, nil)
	if err != nil {
		return nil, &Failure{Kind: FailureIncomplete, Err: err}
	}
	caps, warnings, err := inspect(ctx, endpoint, client)
	if err != nil {
		if errors.Is(err, incompatible) {
			return nil, &Failure{Kind: FailureIncompatible, Err: err}
		}
		return nil, prometheusInspectionFailure(err)
	}
	status := CompatibilityCompatible
	if len(warnings) != 0 {
		status = CompatibilityPartial
	}
	return &Result{TargetType: targetType, Endpoint: endpoint, Capabilities: caps, Warnings: warnings, Status: status}, nil
}
