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

func inspectFastAPI(ctx context.Context, endpoint string) (*Result, error) {
	u, err := urlshape.ParseHTTP(endpoint)
	if err != nil {
		return nil, fmt.Errorf("FastAPI metrics URL: %w", err)
	}
	if u.User != nil || strings.TrimSpace(endpoint) != endpoint {
		return nil, errors.New("FastAPI metrics URL must exclude user information and surrounding whitespace")
	}
	client, err := prometheus.NewClient(defaultTimeout, collector.DirectRoutePrometheusLimits(), nil)
	if err != nil {
		return nil, &Failure{Kind: FailureIncomplete, Err: err}
	}
	caps, warnings, err := collector.InspectFastAPI(ctx, endpoint, client)
	if err != nil {
		if errors.Is(err, collector.ErrFastAPIIncompatible) {
			return nil, &Failure{Kind: FailureIncompatible, Err: err}
		}
		return nil, prometheusInspectionFailure(err)
	}
	status := CompatibilityCompatible
	if len(warnings) != 0 {
		status = CompatibilityPartial
	}
	return &Result{TargetType: TargetFastAPI, Endpoint: endpoint, Capabilities: caps, Warnings: warnings, Status: status}, nil
}
