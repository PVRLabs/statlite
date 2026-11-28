package collector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const nodeDuration = "http_request_duration_seconds"

type nodeProfile struct {
	source string
	// httpStatus returns the status label or an empty string for an excluded
	// tuple, and reports whether it represents an unmatched route.
	// Labels here exclude the bucket boundary.
	httpStatus func([]prometheus.Label) (status string, unmatched bool, err error)
	// A cold registry can have exporter or runtime samples before its first
	// HTTP observation.
	coldMarker   string
	coldRuntime  bool
	scopeWarning string
}

type nodeCollector struct {
	targetName, endpoint string
	client               *prometheus.Client
	profile              nodeProfile
	mu                   sync.Mutex
	previous             requestBaseline
	previousSums         map[string]float64
	process              prometheusProcessBaseline
}

func (c *nodeCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := &CollectionResult{
		TargetName: c.targetName, MetricsSource: c.profile.source,
		TracksCounterContinuity: true, PollStartedAt: time.Now().UTC(),
	}
	defer func() { result.PollFinishedAt = time.Now().UTC() }()

	s, err := scrapeNode(ctx, c.endpoint, c.client, c.profile)
	if err != nil {
		c.previous, c.previousSums, c.process = requestBaseline{}, nil, prometheusProcessBaseline{}
		result.addEvent(EventSeverityError, "node_metrics_unavailable", "", err.Error())
		return result, err
	}
	c.process = s.process.emit(result, c.process, result.PollStartedAt)
	if s.heap.valid {
		result.addSample("runtime_heap_used_bytes", MetricKindGauge, s.heap.value, "bytes")
	}
	if c.profile.scopeWarning != "" && !s.unmatched {
		result.addEvent(EventSeverityWarning, "node_route_scope_unverified", "http_requests_total", c.profile.scopeWarning)
	}
	if len(s.requests) == 0 {
		result.CounterContinuityBreak = c.previous.tuples != nil
		c.previous, c.previousSums = requestBaseline{}, nil
		result.addEvent(EventSeverityWarning, "node_requests_unavailable", "http_requests_total", nodeColdWarning)
		return result, nil
	}

	s.totals.emit(result)
	current := requestBaseline{identity: c.process.identity, tuples: s.requests}
	sameProcess := current.identity > 0 && current.identity == c.previous.identity
	result.CounterContinuityBreak = c.previous.breaksContinuity(current, sameProcess)
	duration, valid := s.durationTotal()
	if valid && !result.CounterContinuityBreak {
		for key, old := range c.previousSums {
			if s.sums[key] < old || s.requests[key].count == c.previous.tuples[key].count && s.sums[key] != old {
				valid = false
				break
			}
		}
	}
	c.previous, c.previousSums = current, nil
	if valid {
		result.addSample("http_request_time_total_seconds", MetricKindCounter, duration, "seconds")
		c.previousSums = s.sums
	} else {
		result.addEvent(EventSeverityWarning, "node_latency_unavailable", "http_request_time_total_seconds", nodeLatencyWarning)
	}
	return result, nil
}

const (
	nodeColdWarning    = "Send a request to an application route to initialize HTTP metrics"
	nodeLatencyWarning = "HTTP duration sums lost continuity or do not match the completed response tuples"
)

type nodeScrape struct {
	requests    map[string]httpRequestTuple
	sums        map[string]float64
	totals      prometheusRequestTotals
	process     prometheusProcess
	heap        prometheusUnlabelledValue
	marker      prometheusUnlabelledValue
	histogram   bool
	observed    bool
	invalidSums bool
	unmatched   bool
}

var errNodeIncompatible = errors.New("expected Node.js exporter histogram or cold registry was not observed")

func scrapeNode(ctx context.Context, endpoint string, client *prometheus.Client, profile nodeProfile) (*nodeScrape, error) {
	if client == nil {
		return nil, errors.New("Node.js exporter requires a metrics client")
	}
	s := &nodeScrape{
		requests: make(map[string]httpRequestTuple),
		sums:     make(map[string]float64),
		// prom-client rounds the process start timestamp to the nearest second.
		process: prometheusProcess{startTimeSlack: 500 * time.Millisecond},
	}
	if _, err := client.Scrape(ctx, endpoint, func(sample prometheus.Sample) error {
		switch sample.Name {
		case nodeDuration + "_count", nodeDuration + "_sum":
			s.observed = true
			return s.acceptHTTP(sample, profile)
		case nodeDuration + "_bucket":
			s.histogram = true
		case "nodejs_heap_size_used_bytes":
			s.heap.accept(sample)
		case profile.coldMarker:
			s.marker.accept(sample)
		default:
			s.process.accept(sample)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("scraping %s metrics: %w", profile.source, err)
	}
	if !s.histogram {
		cold := s.marker.valid && s.marker.value == 1 ||
			profile.coldRuntime && s.heap.valid && s.process.cpu.valid && s.process.startTime(time.Now()) != nil
		if s.observed || !cold {
			return nil, errNodeIncompatible
		}
	}
	return s, nil
}

func (s *nodeScrape) acceptHTTP(sample prometheus.Sample, profile nodeProfile) error {
	isCount := sample.Name == nodeDuration+"_count"
	status, unmatched, err := profile.httpStatus(sample.Labels)
	if err == nil && status == "" {
		return nil
	}
	code, grouped, valid := prometheusHTTPStatus(status)
	if err == nil && (!valid || grouped) {
		err = errors.New("Node.js histogram requires exact numeric status codes")
	}
	if err != nil {
		if isCount {
			return err
		}
		s.invalidSums = true
		return nil
	}
	key := prometheusTupleKey(sample.Labels)
	if isCount {
		if _, exists := s.requests[key]; exists || len(s.requests) >= maxPrometheusHTTPTuples ||
			!validPrometheusCount(sample.Value) || !s.totals.add(sample.Value, code, false) {
			return errors.New("invalid, duplicate, or excessive Node.js response count")
		}
		s.requests[key] = httpRequestTuple{count: sample.Value}
		s.unmatched = s.unmatched || unmatched && sample.Value > 0
	} else if _, exists := s.sums[key]; exists || len(s.sums) >= maxPrometheusHTTPTuples || !finiteNonnegative(sample.Value) {
		s.invalidSums = true
	} else {
		s.sums[key] = sample.Value
	}
	return nil
}

func (s *nodeScrape) durationTotal() (float64, bool) {
	if s.invalidSums || len(s.requests) == 0 || len(s.requests) != len(s.sums) {
		return 0, false
	}
	keys := make([]string, 0, len(s.requests))
	for key, tuple := range s.requests {
		if sum, exists := s.sums[key]; !exists || tuple.count == 0 && sum != 0 {
			return 0, false
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var total float64
	for _, key := range keys {
		total += s.sums[key]
	}
	return total, finiteNonnegative(total)
}

func inspectNode(ctx context.Context, endpoint string, client *prometheus.Client, profile nodeProfile) (capabilities, warnings []string, err error) {
	s, err := scrapeNode(ctx, endpoint, client, profile)
	if err != nil {
		return nil, nil, err
	}
	capabilities = []string{"metrics"}
	if profile.scopeWarning != "" && !s.unmatched {
		warnings = append(warnings, profile.scopeWarning)
	}
	if len(s.requests) == 0 {
		warnings = append(warnings, nodeColdWarning)
	} else {
		capabilities = append(capabilities, "requests", "http_4xx", "http_5xx", "http_404")
		if _, valid := s.durationTotal(); valid {
			capabilities = append(capabilities, "latency")
		} else {
			warnings = append(warnings, nodeLatencyWarning)
		}
	}
	if s.process.startTime(time.Now()) != nil {
		capabilities = append(capabilities, "process_start_time")
		if s.process.cpu.valid {
			capabilities = append(capabilities, "process_cpu_usage")
		}
	}
	if s.heap.valid {
		capabilities = append(capabilities, "runtime_heap_used_bytes")
	}
	return capabilities, warnings, nil
}
