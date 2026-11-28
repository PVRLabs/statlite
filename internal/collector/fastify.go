package collector

import (
	"context"
	"errors"
	"net/url"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const FastifyMetricsSource = "fastify-metrics"

var ErrFastifyIncompatible = errors.New("expected the fastify-metrics histogram with method, route, and status_code labels")

func fastifyProfile(endpoint string) nodeProfile {
	scrapeRoute := "/"
	if u, err := url.Parse(endpoint); err == nil && u.Path != "" {
		scrapeRoute = u.Path
	}
	return nodeProfile{
		source:       FastifyMetricsSource,
		coldRuntime:  true,
		scopeWarning: "Fastify unmatched-route traffic has not been observed. Counts cover only instrumented routes. Set registeredRoutesOnly: false and send a request to an unmatched URL to verify coverage.",
		httpStatus: func(labels []prometheus.Label) (string, bool, error) {
			var method, route, status string
			for _, label := range labels {
				switch label.Name {
				case "method":
					method = label.Value
				case "route":
					route = label.Value
				case "status_code":
					status = label.Value
				}
			}
			if len(labels) != 3 || method == "" || route == "" || status == "" {
				return "", false, ErrFastifyIncompatible
			}
			if route == scrapeRoute {
				return "", false, nil
			}
			return status, route == "__unknown__", nil
		},
	}
}

type FastifyCollector struct{ nodeCollector }

func NewFastifyCollector(targetName, endpoint string, client *prometheus.Client) *FastifyCollector {
	return &FastifyCollector{nodeCollector{targetName: targetName, endpoint: endpoint, client: client, profile: fastifyProfile(endpoint)}}
}

func InspectFastify(ctx context.Context, endpoint string, client *prometheus.Client) (capabilities, warnings []string, err error) {
	capabilities, warnings, err = inspectNode(ctx, endpoint, client, fastifyProfile(endpoint))
	if errors.Is(err, errNodeIncompatible) {
		err = ErrFastifyIncompatible
	}
	return capabilities, warnings, err
}
