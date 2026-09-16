package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

// GoCollector owns the transient full-tuple state needed to avoid attributing
// traffic across missing, invalid, or changing Go histogram populations. It is
// remains bounded to the exact Go HTTP contract validated by this collector.
type GoCollector struct {
	targetName string
	endpoint   string
	client     *prometheus.Client

	mu               sync.Mutex
	continuity       *populationContinuity
	httpObserved     bool
	lastProcessStart *time.Time
	logical          goLogicalCounters
}

type goLogicalCounters struct {
	requests, notFound, clientErrors, serverErrors, duration float64
}

func NewGoCollector(targetName, endpoint string, client *prometheus.Client) *GoCollector {
	maxStates := 0
	if client != nil {
		maxStates = client.MaxAggregationStates()
	}
	return &GoCollector{
		targetName: targetName,
		endpoint:   endpoint,
		client:     client,
		continuity: newPopulationContinuity(maxStates),
	}
}

func (c *GoCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	started := time.Now().UTC()
	result := &CollectionResult{TargetName: c.targetName, PollStartedAt: started}
	defer func() { result.PollFinishedAt = time.Now().UTC() }()
	if c.client == nil || c.endpoint == "" {
		err := errors.New("Go metrics client is not configured")
		result.addEvent(EventSeverityError, "collector_not_configured", "", err.Error())
		c.continuity.breakContinuity()
		return result, err
	}

	n := newGoMetricsNormalizer(c.client.MaxAggregationStates())
	_, scrapeErr := c.client.ScrapeWithMetadata(ctx, c.endpoint, n.acceptMetadata, n.acceptSample)
	if scrapeErr != nil {
		err := fmt.Errorf("scraping Go metrics: %w", scrapeErr)
		result.addEvent(EventSeverityError, "metrics_fetch_failed", "", err.Error())
		c.continuity.breakContinuity()
		return result, err
	}

	evaluation, contractErr := n.finish(started)
	if contractErr != nil {
		optional := n.optionalEvaluation(started)
		result.Samples = optional.samples
		result.ProcessStartTime = optional.processStartTime
		if c.observeProcessRun(optional.processStartTime) {
			c.continuity.resetRun()
			c.logical = goLogicalCounters{}
			c.httpObserved = false
		} else {
			c.continuity.breakContinuity()
		}
		// An empty lazy vector and a runtime-only endpoint are deliberately
		// unconfirmed before HTTP has ever been observed, not recurring errors.
		if len(n.tuples) == 0 && !n.sawFamily[goHTTPFamily] && !c.httpObserved && n.contractErr == nil {
			return result, nil
		}
		result.addEvent(EventSeverityError, "metrics_source_incompatible", "", contractErr.Error())
		return result, contractErr
	}

	result.Samples = evaluation.samples
	result.ProcessStartTime = evaluation.processStartTime
	restarted := c.observeProcessRun(evaluation.processStartTime)
	if restarted {
		c.continuity.resetRun()
		c.logical = goLogicalCounters{}
	}
	c.httpObserved = true
	hadBaseline := len(c.continuity.previous) != 0
	deltas, continuous, err := c.continuity.observeDeltas(evaluation.httpPopulation)
	if err != nil {
		result.Samples = withoutGoHTTPSamples(result.Samples)
		result.addEvent(EventSeverityError, "metrics_source_incompatible", "", err.Error())
		return result, err
	}
	result.Samples = withoutGoHTTPSamples(result.Samples)
	if !continuous {
		if hadBaseline {
			result.addEvent(EventSeverityWarning, "http_continuity_broken", "http_requests_total", "Go HTTP source population changed; a fresh baseline was established")
		}
		return result, nil
	}
	if err := c.addDeltas(deltas); err != nil {
		c.continuity.breakContinuity()
		result.addEvent(EventSeverityError, "metrics_source_incompatible", "", err.Error())
		return result, err
	}
	c.logical.addTo(result)
	return result, nil
}

func (c *GoCollector) observeProcessRun(current *time.Time) bool {
	if current == nil {
		return false
	}
	restarted := c.lastProcessStart != nil && !current.Equal(*c.lastProcessStart)
	cloned := *current
	c.lastProcessStart = &cloned
	return restarted
}

func (c *GoCollector) addDeltas(deltas map[string]populationValue) error {
	next := c.logical
	for identity, delta := range deltas {
		var ok bool
		if next.requests, ok = addExactCount(next.requests, delta.count); !ok {
			return fmt.Errorf("%w: logical request-count overflow", errGoMetricsContract)
		}
		if next.duration, ok = addFiniteNonnegative(next.duration, delta.sum); !ok {
			return fmt.Errorf("%w: logical duration overflow", errGoMetricsContract)
		}
		parts := strings.Split(identity, "\x00")
		code := parts[len(parts)-1]
		if code == "404" {
			if next.notFound, ok = addExactCount(next.notFound, delta.count); !ok {
				return fmt.Errorf("%w: logical 404-count overflow", errGoMetricsContract)
			}
		}
		if code >= "400" && code <= "499" {
			if next.clientErrors, ok = addExactCount(next.clientErrors, delta.count); !ok {
				return fmt.Errorf("%w: logical 4xx-count overflow", errGoMetricsContract)
			}
		}
		if code >= "500" {
			if next.serverErrors, ok = addExactCount(next.serverErrors, delta.count); !ok {
				return fmt.Errorf("%w: logical 5xx-count overflow", errGoMetricsContract)
			}
		}
	}
	c.logical = next
	return nil
}

func (v goLogicalCounters) addTo(result *CollectionResult) {
	result.addSample("http_requests_total", MetricKindCounter, v.requests, "requests")
	result.addSample("http_404_total", MetricKindCounter, v.notFound, "requests")
	result.addSample("http_4xx_total", MetricKindCounter, v.clientErrors, "requests")
	result.addSample("http_5xx_total", MetricKindCounter, v.serverErrors, "requests")
	result.addSample("http_request_time_total_seconds", MetricKindCounter, v.duration, "seconds")
}

func withoutGoHTTPSamples(samples []MetricSample) []MetricSample {
	kept := samples[:0]
	for _, sample := range samples {
		switch sample.Key {
		case "http_requests_total", "http_404_total", "http_4xx_total", "http_5xx_total", "http_request_time_total_seconds":
			continue
		default:
			kept = append(kept, sample)
		}
	}
	return kept
}
