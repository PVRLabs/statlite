package collector

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const FastAPIMetricsSource = "prometheus-fastapi-instrumentator"

const (
	fastAPIRequests       = "http_requests_total"
	fastAPICount          = "http_request_duration_seconds_count"
	fastAPISum            = "http_request_duration_seconds_sum"
	fastAPISignatureCount = "http_request_duration_highr_seconds_count"
	fastAPISignatureSum   = "http_request_duration_highr_seconds_sum"
)

var ErrFastAPIIncompatible = errors.New("prometheus-fastapi-instrumentator default metric signature was not observed")

type FastAPICollector struct {
	targetName, endpoint string
	client               *prometheus.Client
	mu                   sync.Mutex
	previous             requestBaseline
	previousDurations    map[fastAPIRoute]prometheusDuration
	process              pythonProcessBaseline
}

// FastAPIPrometheusLimits requires the actual scrape route so its handler
// can be excluded from the request and duration populations.
func FastAPIPrometheusLimits() prometheus.Limits {
	limits := prometheus.DefaultLimits
	limits.MaxRedirects = 0
	return limits
}

func NewFastAPICollector(targetName, endpoint string, client *prometheus.Client) *FastAPICollector {
	return &FastAPICollector{targetName: targetName, endpoint: endpoint, client: client}
}

func (c *FastAPICollector) Collect(ctx context.Context) (*CollectionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := &CollectionResult{
		TargetName: c.targetName, MetricsSource: FastAPIMetricsSource,
		TracksCounterContinuity: true, PollStartedAt: time.Now().UTC(),
	}
	defer func() { result.PollFinishedAt = time.Now().UTC() }()

	s, err := scrapeFastAPI(ctx, c.endpoint, c.client)
	if err != nil {
		c.previous, c.previousDurations, c.process = requestBaseline{}, nil, pythonProcessBaseline{}
		result.addEvent(EventSeverityError, "fastapi_metrics_unavailable", "", err.Error())
		return result, err
	}

	c.process = s.process.emit(result, c.process, result.PollStartedAt)
	if len(s.requests) == 0 {
		result.CounterContinuityBreak = c.previous.tuples != nil
		c.previous, c.previousDurations = requestBaseline{}, nil
		result.addEvent(EventSeverityWarning, "fastapi_requests_unavailable", "http_requests_total",
			"Send a request to an application route to initialize FastAPI HTTP metrics")
		return result, nil
	}
	s.totals.emit(result)

	scope := "exact"
	if s.totals.grouped {
		scope = "grouped"
	}
	current := requestBaseline{scope: scope, identity: c.process.identity, tuples: s.requests}
	// The default Instrumentator creates label children lazily at zero and
	// retains them for the process lifetime. Require a stable process identity
	// before counting the first observed value of a new tuple.
	sameProcess := current.identity > 0 && current.identity == c.previous.identity
	result.CounterContinuityBreak = c.previous.breaksContinuity(current, sameProcess)
	duration, compatible := s.durationTotal()
	if compatible && !result.CounterContinuityBreak {
		for route, old := range c.previousDurations {
			next := s.durations[route]
			if next.sum < old.sum || next.count == old.count && next.sum != old.sum {
				compatible = false
				break
			}
		}
	}
	c.previous, c.previousDurations = current, nil
	if compatible {
		result.addSample("http_request_time_total_seconds", MetricKindCounter, duration, "seconds")
		c.previousDurations = s.durations
	} else {
		result.addEvent(EventSeverityWarning, "fastapi_latency_unavailable", "http_request_time_total_seconds",
			"FastAPI duration counters are missing, invalid, or do not match the scoped request population")
	}
	return result, nil
}

type fastAPIRoute struct{ handler, method string }

type fastAPIScrape struct {
	scrapeHandler                string
	requests                     map[string]httpRequestTuple
	routeCounts                  map[fastAPIRoute]float64
	durations                    map[fastAPIRoute]prometheusDuration
	badDurations                 bool
	signatureCount, signatureSum prometheusUnlabelledValue
	totals                       pythonRequestTotals
	process                      pythonProcess
}

func scrapeFastAPI(ctx context.Context, endpoint string, client *prometheus.Client) (*fastAPIScrape, error) {
	u, err := url.Parse(endpoint)
	if err != nil || client == nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("FastAPI metrics URL and client are required")
	}
	handler := u.Path
	if handler == "" {
		handler = "/"
	}
	s := &fastAPIScrape{
		scrapeHandler: handler, requests: make(map[string]httpRequestTuple),
		routeCounts: make(map[fastAPIRoute]float64), durations: make(map[fastAPIRoute]prometheusDuration),
	}
	if _, err := client.Scrape(ctx, endpoint, s.accept); err != nil {
		return nil, fmt.Errorf("scraping FastAPI metrics: %w", err)
	}
	// Shared HTTP family names and Python process metrics do not identify
	// this profile. The unlabelled high-resolution histogram exists even
	// before application traffic and is used only for recognition.
	if !s.signatureCount.valid || !s.signatureSum.valid || !validPrometheusCount(s.signatureCount.value) ||
		s.signatureCount.value == 0 && s.signatureSum.value != 0 {
		return nil, ErrFastAPIIncompatible
	}
	return s, nil
}

func (s *fastAPIScrape) accept(sample prometheus.Sample) error {
	switch sample.Name {
	case fastAPISignatureCount:
		s.signatureCount.accept(sample)
	case fastAPISignatureSum:
		s.signatureSum.accept(sample)
	case fastAPIRequests, fastAPICount, fastAPISum:
		return s.acceptHTTP(sample)
	default:
		s.process.accept(sample)
	}
	return nil
}

func (s *fastAPIScrape) acceptHTTP(sample prometheus.Sample) error {
	var route fastAPIRoute
	var status string
	for _, label := range sample.Labels {
		switch label.Name {
		case "handler":
			route.handler = label.Value
		case "method":
			route.method = label.Value
		case "status":
			status = label.Value
		}
	}
	if route.handler == s.scrapeHandler {
		return nil
	}
	validRoute := route.handler != "" && route.method != ""
	if sample.Name == fastAPIRequests {
		code, grouped, valid := pythonStatus(status)
		if !validRoute || len(sample.Labels) != 3 || !valid || !validPrometheusCount(sample.Value) {
			return errors.New("invalid FastAPI request tuple")
		}
		key := prometheusTupleKey(sample.Labels)
		if _, exists := s.requests[key]; exists {
			return errors.New("duplicate FastAPI request tuple")
		}
		if len(s.requests) >= pythonMaxTuples || !s.totals.add(sample.Value, code, grouped) {
			return errors.New("FastAPI request population exceeds tuple or exact count limit")
		}
		s.requests[key] = httpRequestTuple{count: sample.Value}
		s.routeCounts[route] += sample.Value
		return nil
	}
	if !s.badDurations {
		s.badDurations = !validRoute || len(sample.Labels) != 2 || !s.addDuration(route, sample)
	}
	return nil
}

func (s *fastAPIScrape) addDuration(route fastAPIRoute, sample prometheus.Sample) bool {
	tuple, exists := s.durations[route]
	if !exists && len(s.durations) >= pythonMaxTuples {
		return false
	}
	if !tuple.accept(sample.Name == fastAPICount, sample.Value) {
		return false
	}
	s.durations[route] = tuple
	return true
}

func (s *fastAPIScrape) durationTotal() (float64, bool) {
	if s.badDurations || len(s.routeCounts) == 0 || len(s.durations) != len(s.routeCounts) {
		return 0, false
	}
	routes := make([]fastAPIRoute, 0, len(s.durations))
	for route, tuple := range s.durations {
		count, ok := s.routeCounts[route]
		if !ok || !tuple.hasCount || !tuple.hasSum || tuple.count != count || tuple.count == 0 && tuple.sum != 0 {
			return 0, false
		}
		routes = append(routes, route)
	}
	slices.SortFunc(routes, func(a, b fastAPIRoute) int {
		if a.handler != b.handler {
			return cmp.Compare(a.handler, b.handler)
		}
		return cmp.Compare(a.method, b.method)
	})
	var total float64
	for _, route := range routes {
		total += s.durations[route].sum
	}
	return total, finiteNonnegative(total)
}

func InspectFastAPI(ctx context.Context, endpoint string, client *prometheus.Client) (capabilities, warnings []string, err error) {
	s, err := scrapeFastAPI(ctx, endpoint, client)
	if err != nil {
		return nil, nil, err
	}
	capabilities = []string{"metrics"}
	if len(s.requests) == 0 {
		warnings = append(warnings, "send a request to an application route to initialize FastAPI HTTP metrics")
	} else {
		capabilities = append(capabilities, "requests", "http_4xx", "http_5xx")
		if s.totals.grouped {
			warnings = append(warnings, "exact 404 is unavailable with grouped FastAPI statuses")
		} else {
			capabilities = append(capabilities, "http_404")
		}
		if _, ok := s.durationTotal(); ok {
			capabilities = append(capabilities, "latency")
		} else {
			warnings = append(warnings, "latency is unavailable for the scoped FastAPI request population")
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
