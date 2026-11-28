package collector

import (
	"context"
	"errors"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const ExpressMetricsSource = "express-prom-bundle"

var expressProfile = nodeProfile{
	source:     ExpressMetricsSource,
	coldMarker: "up",
	httpStatus: func(labels []prometheus.Label) (string, bool, error) {
		// In the default bundle configuration the scrape bypasses the timer.
		// Route-labelled populations require a different profile.
		if len(labels) != 1 || labels[0].Name != "status_code" || labels[0].Value == "" {
			return "", false, ErrExpressIncompatible
		}
		return labels[0].Value, false, nil
	},
}

var ErrExpressIncompatible = errors.New("expected the default express-prom-bundle histogram with only the status_code label")

type ExpressCollector struct{ nodeCollector }

func NewExpressCollector(targetName, endpoint string, client *prometheus.Client) *ExpressCollector {
	return &ExpressCollector{nodeCollector{targetName: targetName, endpoint: endpoint, client: client, profile: expressProfile}}
}

func InspectExpress(ctx context.Context, endpoint string, client *prometheus.Client) (capabilities, warnings []string, err error) {
	capabilities, warnings, err = inspectNode(ctx, endpoint, client, expressProfile)
	if errors.Is(err, errNodeIncompatible) {
		err = ErrExpressIncompatible
	}
	return capabilities, warnings, err
}
