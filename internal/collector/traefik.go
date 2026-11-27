package collector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const TraefikMetricsSource = "traefik"
const traefikMaxTuples = 256

const (
	traefikRequests = "traefik_service_requests_total"
	traefikCount    = "traefik_service_request_duration_seconds_count"
	traefikSum      = "traefik_service_request_duration_seconds_sum"
)

var ErrTraefikIncompatible = errors.New("Traefik metric families were not observed")

type TraefikCollector struct {
	targetName, endpoint, service string
	client                        *prometheus.Client
	mu                            sync.Mutex
	previous                      requestBaseline
	previousDurations             map[string]traefikDuration
	previousReloadCount           float64
}

func NewTraefikCollector(targetName, endpoint, service string, client *prometheus.Client) *TraefikCollector {
	return &TraefikCollector{targetName: targetName, endpoint: endpoint, service: service, client: client}
}

func (c *TraefikCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	result := &CollectionResult{
		TargetName: c.targetName, MetricsSource: TraefikMetricsSource,
		TracksCounterContinuity: true, PollStartedAt: time.Now().UTC(),
	}
	defer func() { result.PollFinishedAt = time.Now().UTC() }()
	fail := func(err error) (*CollectionResult, error) {
		c.previous, c.previousDurations = requestBaseline{}, nil
		result.addEvent(EventSeverityError, "traefik_http_unavailable", "", err.Error())
		return result, err
	}
	scrape, err := scrapeTraefik(ctx, c.endpoint, c.service, c.client)
	if err != nil {
		return fail(err)
	}
	if len(scrape.requests) == 0 {
		return fail(fmt.Errorf("HTTP request metrics not observed for Traefik service %q; check the exact service name and per-service metrics, then send a request through the service", c.service))
	}
	if err := scrape.addRequests(result); err != nil {
		return fail(err)
	}

	current := requestBaseline{scope: c.service, identity: scrape.proxyStart, tuples: scrape.requests}
	// Configuration reloads can replace the selected population before its
	// stale series disappear from the exporter.
	reloadChanged := c.previousReloadCount != scrape.reloadCount
	// Traefik 3.8.0 creates service counters on first use and retires them
	// on configuration changes. Require both identity signals in both polls.
	newTuplesStartAtZero := current.identity > 0 && current.identity == c.previous.identity &&
		scrape.reloadCount >= 0 && !reloadChanged
	result.CounterContinuityBreak = reloadChanged || c.previous.breaksContinuity(current, newTuplesStartAtZero)
	c.previousReloadCount = scrape.reloadCount
	duration, compatible := scrape.durationTotal()
	if compatible && !result.CounterContinuityBreak {
		for key, old := range c.previousDurations {
			next := scrape.durations[key]
			if next.sum < old.sum || next.count == old.count && next.sum != old.sum {
				compatible = false
				break
			}
		}
	}
	c.previous, c.previousDurations = current, nil
	if compatible {
		result.addSample("http_request_time_total_seconds", MetricKindCounter, duration, "seconds")
		c.previousDurations = scrape.durations
	} else {
		result.addEvent(EventSeverityWarning, "traefik_latency_unavailable", "http_request_time_total_seconds",
			"Traefik duration counters do not match the service request population or have decreased")
	}
	return result, nil
}

type traefikDuration struct {
	count, sum       float64
	hasCount, hasSum bool
}

type traefikScrape struct {
	service         string
	requests        map[string]httpRequestTuple
	durations       map[string]traefikDuration
	badDurations    bool
	recognized      bool
	proxyStart      float64
	proxyStartSeen  bool
	reloadCount     float64
	reloadCountSeen bool
}

func scrapeTraefik(ctx context.Context, endpoint, service string, client *prometheus.Client) (*traefikScrape, error) {
	if client == nil || endpoint == "" || service == "" {
		return nil, errors.New("Traefik metrics URL, service and client are required")
	}
	s := &traefikScrape{
		service: service, requests: make(map[string]httpRequestTuple),
		durations: make(map[string]traefikDuration), reloadCount: -1,
	}
	if _, err := client.Scrape(ctx, endpoint, s.accept); err != nil {
		return nil, fmt.Errorf("scraping Traefik metrics: %w", err)
	}
	if !s.recognized {
		return nil, ErrTraefikIncompatible
	}
	return s, nil
}

func (s *traefikScrape) accept(sample prometheus.Sample) error {
	switch sample.Name {
	case "process_start_time_seconds":
		// Proxy identity is useful for counter continuity only.
		if len(sample.Labels) == 0 {
			if !s.proxyStartSeen && finiteNonnegative(sample.Value) {
				s.proxyStart = sample.Value
			} else {
				s.proxyStart = 0
			}
			s.proxyStartSeen = true
		}
		return nil
	case "traefik_config_reloads_total":
		// This Traefik-specific counter is present before service traffic.
		if len(sample.Labels) == 0 {
			valid := validPrometheusCount(sample.Value)
			s.recognized = s.recognized || valid
			if !s.reloadCountSeen && valid {
				s.reloadCount = sample.Value
			} else {
				s.reloadCount = -1
			}
			s.reloadCountSeen = true
		}
		return nil
	case traefikRequests, traefikCount, traefikSum:
	default:
		return nil
	}

	var service, codeLabel, method, protocol string
	for _, label := range sample.Labels {
		switch label.Name {
		case "service":
			service = label.Value
		case "code":
			codeLabel = label.Value
		case "method":
			method = label.Value
		case "protocol":
			protocol = label.Value
		}
	}
	code, err := strconv.Atoi(codeLabel)
	validStatus := codeLabel == "0" || err == nil && len(codeLabel) == 3 && code >= 100 && code <= 599
	validScope := service != "" && method != "" && protocol != "" && validStatus
	if sample.Name == traefikRequests && validScope && validPrometheusCount(sample.Value) {
		s.recognized = true
	}
	if service != "" && service != s.service {
		return nil
	}
	if sample.Name == traefikRequests {
		if !validScope || !validPrometheusCount(sample.Value) {
			return errors.New("invalid Traefik service request tuple")
		}
		key := prometheusTupleKey(sample.Labels)
		if _, exists := s.requests[key]; exists {
			return errors.New("duplicate Traefik service request tuple")
		}
		if len(s.requests) >= traefikMaxTuples {
			return fmt.Errorf("Traefik service exceeds %d request tuples", traefikMaxTuples)
		}
		s.requests[key] = httpRequestTuple{count: sample.Value, code: code}
		return nil
	}
	if !s.badDurations {
		s.badDurations = !validScope || !s.addDuration(sample)
	}
	return nil
}

func (s *traefikScrape) addDuration(sample prometheus.Sample) bool {
	key := prometheusTupleKey(sample.Labels)
	tuple, exists := s.durations[key]
	if !exists && len(s.durations) >= traefikMaxTuples {
		return false
	}
	if sample.Name == traefikCount {
		if tuple.hasCount || !validPrometheusCount(sample.Value) {
			return false
		}
		tuple.count, tuple.hasCount = sample.Value, true
	} else {
		if tuple.hasSum || !finiteNonnegative(sample.Value) {
			return false
		}
		tuple.sum, tuple.hasSum = sample.Value, true
	}
	s.durations[key] = tuple
	return true
}

func (s *traefikScrape) addRequests(result *CollectionResult) error {
	var requests, notFound, clientErrors, serverErrors, unknown float64
	for _, tuple := range s.requests {
		if tuple.count > maxExactPrometheusCount-requests {
			return errors.New("Traefik request total exceeds exact integer range")
		}
		requests += tuple.count
		switch {
		case tuple.code == 0:
			unknown += tuple.count
		case tuple.code == 404:
			notFound += tuple.count
			clientErrors += tuple.count
		case tuple.code >= 500:
			serverErrors += tuple.count
		case tuple.code >= 400:
			clientErrors += tuple.count
		}
	}
	result.addSample("http_requests_total", MetricKindCounter, requests, "requests")
	result.addSample("http_404_total", MetricKindCounter, notFound, "requests")
	result.addSample("http_4xx_total", MetricKindCounter, clientErrors, "requests")
	result.addSample("http_5xx_total", MetricKindCounter, serverErrors, "requests")
	if unknown > 0 {
		result.addEvent(EventSeverityWarning, "traefik_unknown_status", "http_requests_total",
			"Traefik code 0 contributes to requests with an unknown response status")
	}
	return nil
}

func (s *traefikScrape) durationTotal() (float64, bool) {
	if s.badDurations || len(s.requests) == 0 || len(s.durations) != len(s.requests) {
		return 0, false
	}
	keys := make([]string, 0, len(s.durations))
	for key, tuple := range s.durations {
		request, ok := s.requests[key]
		if !ok || !tuple.hasCount || !tuple.hasSum || request.count != tuple.count || tuple.count == 0 && tuple.sum != 0 {
			return 0, false
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var total float64
	for _, key := range keys {
		total += s.durations[key].sum
	}
	return total, finiteNonnegative(total)
}

func InspectTraefik(ctx context.Context, endpoint, service string, client *prometheus.Client) (capabilities, warnings []string, err error) {
	s, err := scrapeTraefik(ctx, endpoint, service, client)
	if err != nil {
		return nil, nil, err
	}
	if len(s.requests) == 0 {
		return nil, []string{fmt.Sprintf("Traefik endpoint recognized, but HTTP request metrics not observed for service %q; check the exact service name and per-service metrics, then send a request through the service", service)}, nil
	}
	var result CollectionResult
	if err := s.addRequests(&result); err != nil {
		return nil, nil, err
	}
	capabilities = []string{"metrics", "requests", "http_404", "http_4xx", "http_5xx"}
	if _, ok := s.durationTotal(); ok {
		capabilities = append(capabilities, "latency")
	} else {
		warnings = append(warnings, "latency is unavailable for the selected service")
	}
	for _, event := range result.Events {
		warnings = append(warnings, event.Message)
	}
	return capabilities, warnings, nil
}
