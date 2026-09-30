// This file validates and normalizes Quarkus Prometheus/OpenMetrics scrapes.
package collector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

// QuarkusCollector performs the single bounded exposition scrape owned by a
// Quarkus polling cycle.
type QuarkusCollector struct {
	targetName             string
	endpoint               string
	client                 *prometheus.Client
	healthClient           *QuarkusHealthClient
	healthStateMu          sync.Mutex
	healthCapability       quarkusHealthCapability
	healthProcessStartTime *time.Time
}

type quarkusHealthCapability uint8

const (
	quarkusHealthUnknown quarkusHealthCapability = iota
	quarkusHealthAvailable
	quarkusHealthAbsent
)

var ErrQuarkusIncompatible = errors.New("Quarkus metrics endpoint does not expose a finite recognized runtime family")

type QuarkusInspection struct {
	Status       string
	Capabilities []string
	Warnings     []string
}

type quarkusEvaluation struct {
	samples          []MetricSample
	events           []CollectorEvent
	processStartTime *time.Time
}

func NewQuarkusCollector(targetName, endpoint string, client *prometheus.Client, healthClient *QuarkusHealthClient) *QuarkusCollector {
	return &QuarkusCollector{targetName: targetName, endpoint: endpoint, client: client, healthClient: healthClient}
}

func InspectQuarkus(ctx context.Context, endpoint string, client *prometheus.Client) (*QuarkusInspection, error) {
	evaluation, err := NewQuarkusCollector("", endpoint, client, nil).evaluate(ctx)
	if err != nil {
		return nil, err
	}

	inspection := &QuarkusInspection{Status: "compatible"}
	for _, sample := range evaluation.samples {
		inspection.Capabilities = append(inspection.Capabilities, sample.Key)
	}
	for _, event := range evaluation.events {
		if event.Severity == EventSeverityWarning {
			inspection.Warnings = append(inspection.Warnings, event.Message)
		}
	}
	if len(inspection.Warnings) > 0 {
		inspection.Status = "partial"
	}
	return inspection, nil
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
	evaluation, metricsErr := c.evaluate(ctx)
	if metricsErr != nil {
		if errors.Is(metricsErr, ErrQuarkusIncompatible) {
			result.addEvent(EventSeverityError, "metrics_source_incompatible", "", metricsErr.Error())
		} else {
			result.addEvent(EventSeverityError, "metrics_fetch_failed", "", metricsErr.Error())
		}
	} else {
		result.Samples = evaluation.samples
		result.Events = append(result.Events, evaluation.events...)
		result.ProcessStartTime = evaluation.processStartTime
	}

	if c.healthClient != nil {
		if c.shouldProbeHealth(result.ProcessStartTime) {
			health, err := c.healthClient.Fetch(ctx)
			if err != nil {
				if c.healthClient.notFoundOptional && errors.Is(err, ErrQuarkusHealthNotFound) && c.health404IsOptional() {
					if metricsErr == nil {
						c.markHealthAbsent(result.ProcessStartTime)
					}
				} else {
					result.addEvent(EventSeverityWarning, "health_fetch_failed", "", err.Error())
				}
			} else {
				result.HealthStatus = health.Status
				result.DBHealthStatus = health.DBStatus()
				c.markHealthAvailable(result.ProcessStartTime)
			}
		}
	}
	return result, metricsErr
}

func (c *QuarkusCollector) shouldProbeHealth(processStartTime *time.Time) bool {
	c.healthStateMu.Lock()
	defer c.healthStateMu.Unlock()
	if processStartTime != nil {
		if c.healthProcessStartTime != nil && !processStartTime.Equal(*c.healthProcessStartTime) {
			c.healthCapability = quarkusHealthUnknown
		}
		c.healthProcessStartTime = cloneTime(processStartTime)
	}
	if c.healthCapability == quarkusHealthAbsent {
		return false
	}
	return true
}

func (c *QuarkusCollector) markHealthAbsent(processStartTime *time.Time) {
	c.healthStateMu.Lock()
	defer c.healthStateMu.Unlock()
	c.healthCapability = quarkusHealthAbsent
	c.healthProcessStartTime = cloneTime(processStartTime)
}

func (c *QuarkusCollector) health404IsOptional() bool {
	c.healthStateMu.Lock()
	defer c.healthStateMu.Unlock()
	return c.healthCapability == quarkusHealthUnknown
}

func (c *QuarkusCollector) markHealthAvailable(processStartTime *time.Time) {
	c.healthStateMu.Lock()
	defer c.healthStateMu.Unlock()
	c.healthCapability = quarkusHealthAvailable
	c.healthProcessStartTime = cloneTime(processStartTime)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func (c *QuarkusCollector) evaluate(ctx context.Context) (*quarkusEvaluation, error) {
	if c.client == nil || c.endpoint == "" {
		return nil, errors.New("Quarkus metrics client is not configured")
	}
	httpValues := micrometerHTTPValues{}
	runtimeValues := micrometerRuntimeValues{}
	_, err := c.client.Scrape(ctx, c.endpoint, func(sample prometheus.Sample) error {
		switch sample.Name {
		case "http_server_requests_seconds_count":
			httpValues.acceptCount(sample, validateQuarkusHTTPLabels)
			return nil
		case "http_server_requests_seconds_sum":
			httpValues.acceptDuration(sample, validateQuarkusHTTPLabels)
			return nil
		}
		runtimeValues.accept(sample)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scraping Quarkus metrics: %w", err)
	}
	if !quarkusRuntimeCompatible(runtimeValues) {
		err := fmt.Errorf("%w", ErrQuarkusIncompatible)
		return nil, err
	}
	normalized := &CollectionResult{}
	httpValues.addTo(normalized)
	addQuarkusHTTPWarnings(normalized, &httpValues)
	runtimeValues.addTo(normalized)
	addQuarkusPartialWarning(normalized, runtimeValues)
	return &quarkusEvaluation{
		samples:          normalized.Samples,
		events:           normalized.Events,
		processStartTime: normalized.ProcessStartTime,
	}, nil
}

func quarkusRuntimeCompatible(v micrometerRuntimeValues) bool {
	return (v.sawCPU && !v.invalidCPU) || (v.sawHeap && !v.invalidHeap) || (v.sawProcessStart && !v.invalidProcessStart)
}

func addQuarkusPartialWarning(result *CollectionResult, runtime micrometerRuntimeValues) {
	invalid := make([]string, 0, 4)
	if runtime.invalidCPU {
		invalid = append(invalid, "process CPU")
	}
	if runtime.invalidHeap {
		invalid = append(invalid, "heap used")
	}
	if runtime.invalidProcessStart {
		invalid = append(invalid, "process start time")
	}
	if runtime.invalidUptime {
		invalid = append(invalid, "process uptime")
	}
	if len(invalid) == 0 {
		return
	}
	slices.Sort(invalid)
	result.addEvent(EventSeverityWarning, "metrics_partial", "", "Quarkus metrics are partial; invalid concepts were omitted: "+strings.Join(invalid, ", "))
}

func quarkusHTTPDimensions(sample prometheus.Sample) (method, outcome, status string, ok bool) {
	var methodOK, outcomeOK, statusOK bool
	for _, label := range sample.Labels {
		switch label.Name {
		case "method":
			method, methodOK = label.Value, true
		case "outcome":
			outcome, outcomeOK = label.Value, true
		case "status":
			status, statusOK = label.Value, true
		}
	}
	return method, outcome, status, methodOK && outcomeOK && statusOK
}

func addQuarkusHTTPWarnings(result *CollectionResult, v *micrometerHTTPValues) {
	durationMatches := v.durationMatchesCounts()
	if v.incompleteCountLabel {
		result.addEvent(EventSeverityWarning, "metric_dimension_invalid", "http_requests_total", "ignored http_server_requests_seconds_count series without valid method, outcome, and status dimensions and a finite nonnegative value; status counters are unavailable")
	}
	if v.incompleteSumLabel {
		result.addEvent(EventSeverityWarning, "metric_dimension_invalid", "http_request_time_total_seconds", "ignored http_server_requests_seconds_sum series without valid method, outcome, and status dimensions and a finite nonnegative value")
	}
	if v.sawDuration && !durationMatches && !v.durationOverflow && !v.countDuplicate && !v.durationDuplicate && !v.matchingOverflow {
		result.addEvent(EventSeverityWarning, "metric_series_mismatch", "http_request_time_total_seconds", "omitted HTTP request duration because its accepted dimensions do not match request count series within the bounded matching state")
	}
	if v.requestOverflow {
		result.addEvent(EventSeverityWarning, "metric_aggregate_invalid", "http_requests_total", "omitted HTTP request count because finite source values overflowed the normalized aggregate")
	}
	for _, status := range []struct {
		key      string
		overflow bool
	}{
		{key: "http_404_total", overflow: v.notFoundOverflow},
		{key: "http_4xx_total", overflow: v.clientErrorsOverflow},
		{key: "http_5xx_total", overflow: v.serverErrorsOverflow},
	} {
		if status.overflow {
			result.addEvent(EventSeverityWarning, "metric_aggregate_invalid", status.key, fmt.Sprintf("omitted %s because finite source values overflowed the normalized aggregate", status.key))
		}
	}
	if v.durationOverflow {
		result.addEvent(EventSeverityWarning, "metric_aggregate_invalid", "http_request_time_total_seconds", "omitted HTTP request duration because finite source values overflowed the normalized aggregate")
	}
	if v.countDuplicate {
		result.addEvent(EventSeverityWarning, "metric_series_duplicate", "http_requests_total", "omitted HTTP request and status counters because a count series identity was repeated")
	}
	if v.durationDuplicate {
		result.addEvent(EventSeverityWarning, "metric_series_duplicate", "http_request_time_total_seconds", "omitted HTTP request duration because a duration series identity was repeated")
	}
	if v.matchingOverflow {
		result.addEvent(EventSeverityWarning, "metric_aggregation_limit", "", "omitted HTTP request, status, and duration concepts because bounded series-identity state was exceeded")
	}
}

func validateQuarkusHTTPLabels(sample prometheus.Sample) (int, bool) {
	method, outcome, status, ok := quarkusHTTPDimensions(sample)
	code, err := strconv.Atoi(status)
	return code, ok && method != "" && outcome != "" && err == nil && code >= 100 && code <= 599
}
