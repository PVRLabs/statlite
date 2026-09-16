package collector

// This file validates the exact Go promhttp histogram contract. Target wiring,
// continuity, and inspection semantics are added separately.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const (
	goHTTPFamily       = "go_http_request_duration_seconds"
	goHeapFamily       = "go_memstats_heap_alloc_bytes"
	goProcessStartName = "process_start_time_seconds"
	maxExactCount      = float64(1<<53 - 1)
)

var errGoMetricsContract = errors.New("Go metrics contract is incompatible")

type goMetricTuple struct{ method, code string }
type goMetricPair struct {
	count, sum       float64
	sawCount, sawSum bool
}

type goMetricsEvaluation struct {
	samples          []MetricSample
	processStartTime *time.Time
	httpPopulation   map[string]populationValue
}

type goMetricsNormalizer struct {
	maxStates                        int
	types                            map[string]string
	sawFamily                        map[string]bool
	tuples                           map[goMetricTuple]goMetricPair
	heap, processStart               float64
	sawHeap, sawProcessStart         bool
	invalidHeap, invalidProcessStart bool
	contractErr                      error
}

func newGoMetricsNormalizer(maxStates int) *goMetricsNormalizer {
	return &goMetricsNormalizer{
		maxStates: maxStates,
		types:     make(map[string]string, 3),
		sawFamily: make(map[string]bool, 3),
		tuples:    make(map[goMetricTuple]goMetricPair),
	}
}

func evaluateGoMetrics(ctx context.Context, endpoint string, client *prometheus.Client, now time.Time) (*goMetricsEvaluation, error) {
	if client == nil || endpoint == "" {
		return nil, errors.New("Go metrics client is not configured")
	}
	n := newGoMetricsNormalizer(client.MaxAggregationStates())
	_, err := client.ScrapeWithMetadata(ctx, endpoint, n.acceptMetadata, n.acceptSample)
	if err != nil {
		return nil, fmt.Errorf("scraping Go metrics: %w", err)
	}
	return n.finish(now)
}

func (n *goMetricsNormalizer) optionalEvaluation(now time.Time) *goMetricsEvaluation {
	result := &CollectionResult{}
	if n.sawHeap && !n.invalidHeap && n.types[goHeapFamily] == "gauge" {
		result.addSample("runtime_heap_used_bytes", MetricKindGauge, n.heap, "bytes")
	}
	if n.sawProcessStart && !n.invalidProcessStart && n.types[goProcessStartName] == "gauge" {
		started := unixSeconds(n.processStart)
		if !now.Before(started) {
			result.ProcessStartTime = &started
			result.addSample("process_start_time", MetricKindGauge, n.processStart, "unix_seconds")
			result.addSample("process_uptime", MetricKindGauge, now.Sub(started).Seconds(), "seconds")
		}
	}
	return &goMetricsEvaluation{samples: result.Samples, processStartTime: result.ProcessStartTime}
}

func (n *goMetricsNormalizer) acceptMetadata(metadata prometheus.Metadata) error {
	want, recognized := map[string]string{
		goHTTPFamily: "histogram", goHeapFamily: "gauge", goProcessStartName: "gauge",
	}[metadata.Family]
	if !recognized {
		return nil
	}
	if n.sawFamily[metadata.Family] {
		n.invalidateFamily(metadata.Family, "TYPE declaration for %s appears after its samples", metadata.Family)
	}
	if _, exists := n.types[metadata.Family]; exists {
		n.invalidateFamily(metadata.Family, "duplicate TYPE declaration for %s", metadata.Family)
	} else {
		n.types[metadata.Family] = metadata.Type
	}
	if metadata.Type != want {
		n.invalidateFamily(metadata.Family, "%s has TYPE %s, want %s", metadata.Family, metadata.Type, want)
	}
	return nil
}

func (n *goMetricsNormalizer) acceptSample(sample prometheus.Sample) error {
	switch sample.Name {
	case goHTTPFamily + "_count", goHTTPFamily + "_sum", goHTTPFamily + "_bucket":
		n.sawFamily[goHTTPFamily] = true
		if sample.Name == goHTTPFamily+"_bucket" {
			return nil
		}
		n.acceptHTTP(sample)
	case goHeapFamily:
		n.sawFamily[goHeapFamily] = true
		if n.sawHeap || len(sample.Labels) != 0 || !finiteNonnegative(sample.Value) {
			n.invalidHeap = true
		} else {
			n.sawHeap, n.heap = true, sample.Value
		}
	case goProcessStartName:
		n.sawFamily[goProcessStartName] = true
		if n.sawProcessStart || len(sample.Labels) != 0 || !finiteNonnegative(sample.Value) || sample.Value == 0 || !rfc3339RoundTripsUnixSeconds(sample.Value) {
			n.invalidProcessStart = true
		} else {
			n.sawProcessStart, n.processStart = true, sample.Value
		}
	case goHTTPFamily, goHTTPFamily + "_total":
		n.invalidate("conflicting sample %s", sample.Name)
	}
	return nil
}

func (n *goMetricsNormalizer) acceptHTTP(sample prometheus.Sample) {
	method, code, ok := goHTTPDimensions(sample.Labels)
	if !ok {
		n.invalidate("%s must have exactly code and method labels", sample.Name)
		return
	}
	if !validGoMethod(method) {
		n.invalidate("invalid Go HTTP method %q", method)
		return
	}
	status, err := strconv.Atoi(code)
	if err != nil || len(code) != 3 || status < 100 || status > 599 {
		n.invalidate("invalid Go HTTP status %q", code)
		return
	}
	if !finiteNonnegative(sample.Value) {
		n.invalidate("%s has a non-finite or negative value", sample.Name)
		return
	}
	tuple := goMetricTuple{method: method, code: code}
	pair, exists := n.tuples[tuple]
	if !exists && len(n.tuples) >= n.maxStates {
		n.invalidate("Go HTTP aggregation-state limit exceeded")
		return
	}
	if sample.Name == goHTTPFamily+"_count" {
		if pair.sawCount || sample.Value != math.Trunc(sample.Value) || sample.Value > maxExactCount {
			n.invalidate("invalid or duplicate Go HTTP count for method=%q code=%q", method, code)
			return
		}
		pair.count, pair.sawCount = sample.Value, true
	} else {
		if pair.sawSum {
			n.invalidate("duplicate Go HTTP sum for method=%q code=%q", method, code)
			return
		}
		pair.sum, pair.sawSum = sample.Value, true
	}
	n.tuples[tuple] = pair
}

func (n *goMetricsNormalizer) finish(now time.Time) (*goMetricsEvaluation, error) {
	if n.contractErr != nil {
		return nil, n.contractErr
	}
	if len(n.tuples) == 0 {
		return nil, fmt.Errorf("%w: required histogram has no observed count/sum population", errGoMetricsContract)
	}
	if n.types[goHTTPFamily] != "histogram" {
		return nil, fmt.Errorf("%w: required histogram is missing its TYPE declaration", errGoMetricsContract)
	}
	var requests, notFound, clientErrors, serverErrors, duration float64
	population := make(map[string]populationValue, len(n.tuples))
	for tuple, pair := range n.tuples {
		if !pair.sawCount || !pair.sawSum {
			return nil, fmt.Errorf("%w: count/sum tuple population does not match", errGoMetricsContract)
		}
		var ok bool
		requests, ok = addExactCount(requests, pair.count)
		if !ok {
			return nil, fmt.Errorf("%w: request-count aggregation overflow", errGoMetricsContract)
		}
		if tuple.code == "404" {
			notFound, ok = addExactCount(notFound, pair.count)
		}
		if tuple.code >= "400" && tuple.code <= "499" {
			clientErrors, ok = addExactCount(clientErrors, pair.count)
		}
		if tuple.code >= "500" {
			serverErrors, ok = addExactCount(serverErrors, pair.count)
		}
		if !ok {
			return nil, fmt.Errorf("%w: status-count aggregation overflow", errGoMetricsContract)
		}
		duration, ok = addFiniteNonnegative(duration, pair.sum)
		if !ok {
			return nil, fmt.Errorf("%w: duration aggregation overflow", errGoMetricsContract)
		}
		population[tuple.method+"\x00"+tuple.code] = populationValue{count: pair.count, sum: pair.sum}
	}
	optional := n.optionalEvaluation(now)
	result := &CollectionResult{Samples: optional.samples, ProcessStartTime: optional.processStartTime}
	result.addSample("http_requests_total", MetricKindCounter, requests, "requests")
	result.addSample("http_404_total", MetricKindCounter, notFound, "requests")
	result.addSample("http_4xx_total", MetricKindCounter, clientErrors, "requests")
	result.addSample("http_5xx_total", MetricKindCounter, serverErrors, "requests")
	result.addSample("http_request_time_total_seconds", MetricKindCounter, duration, "seconds")
	return &goMetricsEvaluation{samples: result.Samples, processStartTime: result.ProcessStartTime, httpPopulation: population}, nil
}

func (n *goMetricsNormalizer) invalidate(format string, args ...any) {
	if n.contractErr == nil {
		n.contractErr = fmt.Errorf("%w: %s", errGoMetricsContract, fmt.Sprintf(format, args...))
	}
}

func (n *goMetricsNormalizer) invalidateFamily(family, format string, args ...any) {
	switch family {
	case goHTTPFamily:
		n.invalidate(format, args...)
	case goHeapFamily:
		n.invalidHeap = true
	case goProcessStartName:
		n.invalidProcessStart = true
	}
}

func goHTTPDimensions(labels []prometheus.Label) (method, code string, ok bool) {
	if len(labels) != 2 {
		return "", "", false
	}
	for _, label := range labels {
		switch label.Name {
		case "method":
			method = label.Value
		case "code":
			code = label.Value
		default:
			return "", "", false
		}
	}
	return method, code, method != "" && code != ""
}

func validGoMethod(method string) bool {
	switch method {
	case "get", "put", "head", "post", "delete", "connect", "options", "notify", "trace", "patch", "unknown":
		return true
	default:
		return false
	}
}

func addExactCount(total, value float64) (float64, bool) {
	result := total + value
	return result, result <= maxExactCount && result == math.Trunc(result)
}
