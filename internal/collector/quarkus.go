// This file validates Quarkus Prometheus/OpenMetrics scrapes before
// Quarkus-specific normalization.
package collector

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

// QuarkusCollector performs the single bounded exposition scrape owned by a
// Quarkus polling cycle. Quarkus normalization is added separately.
type QuarkusCollector struct {
	targetName string
	endpoint   string
	client     *prometheus.Client
}

func NewQuarkusCollector(targetName, endpoint string, client *prometheus.Client) *QuarkusCollector {
	return &QuarkusCollector{targetName: targetName, endpoint: endpoint, client: client}
}

func (c *QuarkusCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	started := time.Now().UTC()
	result := &CollectionResult{TargetName: c.targetName, PollStartedAt: started}
	defer func() { result.PollFinishedAt = time.Now().UTC() }()
	if c.client == nil || c.endpoint == "" {
		err := errors.New("Quarkus metrics client is not configured")
		result.addEvent(EventSeverityError, "collector_not_configured", "", err.Error())
		return result, err
	}

	compatible := false
	_, err := c.client.Scrape(ctx, c.endpoint, func(sample prometheus.Sample) error {
		if math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) {
			return nil
		}
		switch sample.Name {
		case "process_cpu_usage", "process_start_time_seconds":
			compatible = true
		case "jvm_memory_used_bytes":
			for _, label := range sample.Labels {
				if label.Name == "area" && label.Value == "heap" {
					compatible = true
				}
			}
		}
		return nil
	})
	if err != nil {
		wrapped := fmt.Errorf("scraping Quarkus metrics: %w", err)
		result.addEvent(EventSeverityError, "metrics_fetch_failed", "", wrapped.Error())
		return result, wrapped
	}
	if !compatible {
		err := errors.New("Quarkus metrics endpoint does not expose a finite recognized runtime family")
		result.addEvent(EventSeverityError, "metrics_source_incompatible", "", err.Error())
		return result, err
	}
	return result, nil
}
