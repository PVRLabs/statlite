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

const FlaskMetricsSource = "prometheus-flask-exporter"

const (
	flaskRequests = "flask_http_request_total"
	flaskCount    = "flask_http_request_duration_seconds_count"
	flaskSum      = "flask_http_request_duration_seconds_sum"
)

var ErrFlaskIncompatible = errors.New("prometheus-flask-exporter default metric signature was not observed")

type FlaskCollector struct {
	targetName, endpoint string
	client               *prometheus.Client
	mu                   sync.Mutex
	previous             requestBaseline
	previousDurations    map[string]flaskDuration
	process              pythonProcessBaseline
}

func NewFlaskCollector(targetName, endpoint string, client *prometheus.Client) *FlaskCollector {
	return &FlaskCollector{targetName: targetName, endpoint: endpoint, client: client}
}

func (c *FlaskCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := &CollectionResult{
		TargetName: c.targetName, MetricsSource: FlaskMetricsSource,
		TracksCounterContinuity: true, PollStartedAt: time.Now().UTC(),
	}
	defer func() { result.PollFinishedAt = time.Now().UTC() }()

	s, err := scrapeFlask(ctx, c.endpoint, c.client)
	if err != nil {
		c.previous, c.previousDurations, c.process = requestBaseline{}, nil, pythonProcessBaseline{}
		result.addEvent(EventSeverityError, "flask_metrics_unavailable", "", err.Error())
		return result, err
	}
	c.process = s.process.emit(result, c.process, result.PollStartedAt)
	if len(s.requests) == 0 {
		result.CounterContinuityBreak = c.previous.tuples != nil
		c.previous, c.previousDurations = requestBaseline{}, nil
		result.addEvent(EventSeverityWarning, "flask_requests_unavailable", "http_requests_total",
			"Send a request to an application route to initialize Flask HTTP metrics")
		return result, nil
	}
	s.totals.emit(result)
	current := requestBaseline{identity: c.process.identity, tuples: s.requests}
	// The default single-process registry starts new status children at zero.
	sameProcess := current.identity > 0 && current.identity == c.previous.identity
	result.CounterContinuityBreak = c.previous.breaksContinuity(current, sameProcess)
	c.previous = current

	duration, valid := s.durationTotal()
	if valid && !result.CounterContinuityBreak {
		for key, old := range c.previousDurations {
			next, exists := s.durations[key]
			if !exists || next.count < old.count || next.sum < old.sum ||
				next.count == old.count && next.sum != old.sum {
				valid = false
				break
			}
		}
	}
	c.previousDurations = nil
	if valid {
		result.addSample("http_request_time_total_seconds", MetricKindCounter, duration, "seconds")
		c.previousDurations = s.durations
	} else {
		result.addEvent(EventSeverityWarning, "flask_latency_unavailable", "http_request_time_total_seconds",
			"Flask path duration counters exceeded the series limit, lost continuity, or do not match completed responses")
	}
	return result, nil
}

type flaskDuration struct {
	prometheusDuration
	responseKey string
}

type flaskScrape struct {
	signatureSeen, signatureValid bool
	requests                      map[string]httpRequestTuple
	durations                     map[string]flaskDuration
	badDurations                  bool
	totals                        pythonRequestTotals
	process                       pythonProcess
}

func scrapeFlask(ctx context.Context, endpoint string, client *prometheus.Client) (*flaskScrape, error) {
	if client == nil {
		return nil, errors.New("Flask requires a metrics client")
	}
	s := &flaskScrape{
		requests: make(map[string]httpRequestTuple), durations: make(map[string]flaskDuration),
	}
	if _, err := client.Scrape(ctx, endpoint, s.accept); err != nil {
		return nil, fmt.Errorf("scraping Flask metrics: %w", err)
	}
	if !s.signatureValid {
		return nil, ErrFlaskIncompatible
	}
	return s, nil
}

func (s *flaskScrape) accept(sample prometheus.Sample) error {
	switch sample.Name {
	case "flask_exporter_info":
		s.signatureValid = !s.signatureSeen && sample.Value == 1 && len(sample.Labels) == 1 &&
			sample.Labels[0].Name == "version" && sample.Labels[0].Value != ""
		s.signatureSeen = true
	case flaskRequests, flaskCount, flaskSum:
		return s.acceptHTTP(sample)
	default:
		// The exception counter overlaps the completed status population.
		s.process.accept(sample)
	}
	return nil
}

func (s *flaskScrape) acceptHTTP(sample prometheus.Sample) error {
	isRequest := sample.Name == flaskRequests
	if !isRequest && s.badDurations {
		return nil
	}
	var method, status, path string
	for _, label := range sample.Labels {
		switch label.Name {
		case "method":
			method = label.Value
		case "status":
			status = label.Value
		case "path":
			path = label.Value
		}
	}
	code, grouped, valid := pythonStatus(status)
	validResponse := method != "" && valid && !grouped
	responseKey := prometheusTupleKey([]prometheus.Label{{Name: "method", Value: method}, {Name: "status", Value: status}})
	if isRequest {
		if !validResponse || len(sample.Labels) != 2 || !validPrometheusCount(sample.Value) {
			return errors.New("invalid Flask response tuple")
		}
		if _, exists := s.requests[responseKey]; exists {
			return errors.New("duplicate Flask response tuple")
		}
		if len(s.requests) >= pythonMaxTuples || !s.totals.add(sample.Value, code, false) {
			return errors.New("Flask response population exceeds tuple or exact count limit")
		}
		s.requests[responseKey] = httpRequestTuple{count: sample.Value}
		return nil
	}

	if !validResponse || len(sample.Labels) != 3 || path == "" {
		s.badDurations = true
		return nil
	}
	key := prometheusTupleKey(sample.Labels)
	d, exists := s.durations[key]
	if (!exists && len(s.durations) >= pythonMaxTuples) || !d.accept(sample.Name == flaskCount, sample.Value) {
		s.badDurations = true
		return nil
	}
	d.responseKey = responseKey
	s.durations[key] = d
	return nil
}

func (s *flaskScrape) durationTotal() (float64, bool) {
	if s.badDurations || len(s.requests) == 0 || len(s.durations) == 0 {
		return 0, false
	}
	matched := make(map[string]float64, len(s.requests))
	keys := make([]string, 0, len(s.durations))
	for key, d := range s.durations {
		request, exists := s.requests[d.responseKey]
		if !exists || !d.hasCount || !d.hasSum || d.count > request.count-matched[d.responseKey] ||
			d.count == 0 && d.sum != 0 {
			return 0, false
		}
		matched[d.responseKey] += d.count
		keys = append(keys, key)
	}
	for key, request := range s.requests {
		if count, exists := matched[key]; !exists || count != request.count {
			return 0, false
		}
	}
	slices.Sort(keys)
	var total float64
	for _, key := range keys {
		total += s.durations[key].sum
	}
	return total, finiteNonnegative(total)
}

func InspectFlask(ctx context.Context, endpoint string, client *prometheus.Client) (capabilities, warnings []string, err error) {
	s, err := scrapeFlask(ctx, endpoint, client)
	if err != nil {
		return nil, nil, err
	}
	capabilities = []string{"metrics"}
	if len(s.requests) == 0 {
		warnings = append(warnings, "send a request to an application route to initialize Flask HTTP metrics")
	} else {
		capabilities = append(capabilities, "requests", "http_4xx", "http_5xx", "http_404")
		if _, ok := s.durationTotal(); ok {
			capabilities = append(capabilities, "latency")
		} else {
			warnings = append(warnings, "latency is unavailable for the scoped Flask request population")
		}
	}
	if s.process.startTime(time.Now()) != nil {
		capabilities = append(capabilities, "process_start_time")
		if s.process.cpu.valid {
			capabilities = append(capabilities, "process_cpu_usage")
		}
	}
	return capabilities, warnings, nil
}
