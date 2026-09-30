// This file validates and normalizes the explicitly selected Micronaut contract.
package collector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

// MicronautCollector performs one bounded scrape of the exact metrics endpoint.
type MicronautCollector struct {
	targetName string
	endpoint   string
	client     *prometheus.Client
}

var ErrMicronautIncompatible = errors.New("Micronaut metrics endpoint does not expose a finite recognized runtime family")

// MicronautInspection describes supported concepts, not proven framework origin.
type MicronautInspection struct {
	Status       string
	Capabilities []string
	Warnings     []string
}

type micronautEvaluation struct {
	samples          []MetricSample
	events           []CollectorEvent
	processStartTime *time.Time
}

func NewMicronautCollector(targetName, endpoint string, client *prometheus.Client) *MicronautCollector {
	return &MicronautCollector{targetName: targetName, endpoint: endpoint, client: client}
}

func InspectMicronaut(ctx context.Context, endpoint string, client *prometheus.Client) (*MicronautInspection, error) {
	evaluation, err := NewMicronautCollector("", endpoint, client).evaluate(ctx)
	if err != nil {
		return nil, err
	}

	inspection := &MicronautInspection{Status: "compatible"}
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

func (c *MicronautCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	result := &CollectionResult{TargetName: c.targetName, PollStartedAt: time.Now().UTC()}
	defer func() { result.PollFinishedAt = time.Now().UTC() }()
	if c.client == nil || c.endpoint == "" {
		err := errors.New("Micronaut metrics client is not configured")
		result.addEvent(EventSeverityError, "collector_not_configured", "", err.Error())
		return result, err
	}
	evaluation, err := c.evaluate(ctx)
	if err != nil {
		if errors.Is(err, ErrMicronautIncompatible) {
			result.addEvent(EventSeverityError, "metrics_source_incompatible", "", err.Error())
		} else {
			result.addEvent(EventSeverityError, "metrics_fetch_failed", "", err.Error())
		}
		return result, err
	}
	result.Samples = evaluation.samples
	result.Events = evaluation.events
	result.ProcessStartTime = evaluation.processStartTime
	return result, nil
}

func (c *MicronautCollector) evaluate(ctx context.Context) (*micronautEvaluation, error) {
	if c.client == nil || c.endpoint == "" {
		return nil, errors.New("Micronaut metrics client is not configured")
	}
	httpValues := micrometerHTTPValues{}
	runtimeValues := micrometerRuntimeValues{}
	_, err := c.client.Scrape(ctx, c.endpoint, func(sample prometheus.Sample) error {
		switch sample.Name {
		case "http_server_requests_seconds_count":
			httpValues.acceptCount(sample, validateMicronautHTTPLabels)
			return nil
		case "http_server_requests_seconds_sum":
			httpValues.acceptDuration(sample, validateMicronautHTTPLabels)
			return nil
		}
		runtimeValues.accept(sample)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scraping Micronaut metrics: %w", err)
	}
	if !micronautRuntimeCompatible(runtimeValues) {
		err := fmt.Errorf("%w", ErrMicronautIncompatible)
		return nil, err
	}
	normalized := &CollectionResult{}
	httpValues.addTo(normalized)
	addMicronautHTTPWarnings(normalized, &httpValues)
	runtimeValues.addTo(normalized)
	addMicronautPartialWarning(normalized, runtimeValues)
	return &micronautEvaluation{
		samples:          normalized.Samples,
		events:           normalized.Events,
		processStartTime: normalized.ProcessStartTime,
	}, nil
}

func micronautRuntimeCompatible(v micrometerRuntimeValues) bool {
	return (v.sawCPU && !v.invalidCPU) || (v.sawHeap && !v.invalidHeap) || (v.sawProcessStart && !v.invalidProcessStart)
}

func addMicronautPartialWarning(result *CollectionResult, runtime micrometerRuntimeValues) {
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
	result.addEvent(EventSeverityWarning, "metrics_partial", "", "Micronaut metrics are partial; invalid concepts were omitted: "+strings.Join(invalid, ", "))
}

// No trimming, enums, or Quarkus outcome requirement: additional labels remain
// part of transient series identity, but never become normalized dimensions.
func validateMicronautHTTPLabels(sample prometheus.Sample) (int, bool) {
	var method, status, uri, exception string
	for _, label := range sample.Labels {
		switch label.Name {
		case "method":
			method = label.Value
		case "status":
			status = label.Value
		case "uri":
			uri = label.Value
		case "exception":
			exception = label.Value
		}
	}
	if method == "" || uri == "" || exception == "" || len(status) != 3 {
		return 0, false
	}
	code := 0
	for i := 0; i < len(status); i++ {
		if status[i] < '0' || status[i] > '9' {
			return 0, false
		}
		code = code*10 + int(status[i]-'0')
	}
	return code, code >= 100 && code <= 599
}

func addMicronautHTTPWarnings(result *CollectionResult, v *micrometerHTTPValues) {
	durationMatches := v.durationMatchesCounts()
	if v.incompleteCountLabel {
		result.addEvent(EventSeverityWarning, "metric_dimension_invalid", "http_requests_total", "Micronaut metrics: ignored http_server_requests_seconds_count series without valid method, status, uri, and exception dimensions and a finite nonnegative value; status counters are unavailable")
	}
	if v.incompleteSumLabel {
		result.addEvent(EventSeverityWarning, "metric_dimension_invalid", "http_request_time_total_seconds", "Micronaut metrics: ignored http_server_requests_seconds_sum series without valid method, status, uri, and exception dimensions and a finite nonnegative value")
	}
	if v.sawDuration && !durationMatches && !v.durationOverflow && !v.countDuplicate && !v.durationDuplicate && !v.matchingOverflow {
		result.addEvent(EventSeverityWarning, "metric_series_mismatch", "http_request_time_total_seconds", "Micronaut metrics: omitted HTTP request duration because its accepted dimensions do not match request count series within the bounded matching state")
	}
	if v.requestOverflow {
		result.addEvent(EventSeverityWarning, "metric_aggregate_invalid", "http_requests_total", "Micronaut metrics: omitted HTTP request count because finite source values overflowed the normalized aggregate")
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
			result.addEvent(EventSeverityWarning, "metric_aggregate_invalid", status.key, fmt.Sprintf("Micronaut metrics: omitted %s because finite source values overflowed the normalized aggregate", status.key))
		}
	}
	if v.durationOverflow {
		result.addEvent(EventSeverityWarning, "metric_aggregate_invalid", "http_request_time_total_seconds", "Micronaut metrics: omitted HTTP request duration because finite source values overflowed the normalized aggregate")
	}
	if v.countDuplicate {
		result.addEvent(EventSeverityWarning, "metric_series_duplicate", "http_requests_total", "Micronaut metrics: omitted HTTP request and status counters because a count series identity was repeated")
	}
	if v.durationDuplicate {
		result.addEvent(EventSeverityWarning, "metric_series_duplicate", "http_request_time_total_seconds", "Micronaut metrics: omitted HTTP request duration because a duration series identity was repeated")
	}
	if v.matchingOverflow {
		result.addEvent(EventSeverityWarning, "metric_aggregation_limit", "", "Micronaut metrics: omitted HTTP request, status, and duration concepts because bounded series-identity state was exceeded")
	}
}
