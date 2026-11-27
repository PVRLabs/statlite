package collector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pvrlabs/statlite/internal/config"
	"github.com/pvrlabs/statlite/internal/prometheus"
)

const DjangoMetricsSource = "django-prometheus"

const (
	djangoSignature     = "django_http_requests_before_middlewares_total"
	djangoStatuses      = "django_http_responses_total_by_status_total"
	djangoViewStatuses  = "django_http_responses_total_by_status_view_method_total"
	djangoMetricsView   = "prometheus-django-metrics"
	djangoDurationCount = "django_http_requests_latency_including_middlewares_seconds_count"
	djangoDurationSum   = "django_http_requests_latency_including_middlewares_seconds_sum"
)

var ErrDjangoIncompatible = errors.New("endpoint does not match the configured django-prometheus recipe")

type DjangoCollector struct {
	targetName, endpoint, mode string
	client                     *prometheus.Client
	mu                         sync.Mutex
	previous                   requestBaseline
	duration                   prometheusDuration
	process                    pythonProcessBaseline
}

func NewDjangoCollector(targetName, endpoint, mode string, client *prometheus.Client) *DjangoCollector {
	return &DjangoCollector{targetName: targetName, endpoint: endpoint, mode: mode, client: client}
}

func (c *DjangoCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := &CollectionResult{
		TargetName: c.targetName, MetricsSource: DjangoMetricsSource,
		TracksCounterContinuity: true, PollStartedAt: time.Now().UTC(),
	}
	defer func() { result.PollFinishedAt = time.Now().UTC() }()

	s, err := scrapeDjango(ctx, c.endpoint, c.mode, c.client)
	if err != nil {
		c.previous, c.duration, c.process = requestBaseline{}, prometheusDuration{}, pythonProcessBaseline{}
		result.addEvent(EventSeverityError, "django_metrics_unavailable", "", err.Error())
		return result, err
	}
	c.process = s.process.emit(result, c.process, result.PollStartedAt)
	if len(s.requests) == 0 {
		result.CounterContinuityBreak = c.previous.tuples != nil
		c.previous, c.duration = requestBaseline{}, prometheusDuration{}
		result.addEvent(EventSeverityWarning, "django_requests_unavailable", "http_requests_total",
			"Send a request to an application route to initialize Django HTTP metrics")
		return result, nil
	}
	s.totals.emit(result)
	current := requestBaseline{scope: c.mode, identity: c.process.identity, tuples: s.requests}
	// The default single-process registry creates status tuples lazily at zero.
	sameProcess := current.identity > 0 && current.identity == c.previous.identity
	result.CounterContinuityBreak = c.previous.breaksContinuity(current, sameProcess)
	c.previous = current
	if c.mode == config.DjangoMetricsModeSeparate {
		if s.hasLatency() && (result.CounterContinuityBreak || !c.duration.hasCount ||
			(s.duration.sum >= c.duration.sum && (s.duration.count != c.duration.count || s.duration.sum == c.duration.sum))) {
			result.addSample("http_request_time_total_seconds", MetricKindCounter, s.duration.sum, "seconds")
			c.duration = s.duration
		} else {
			c.duration = prometheusDuration{}
			result.addEvent(EventSeverityWarning, "django_latency_unavailable", "http_request_time_total_seconds",
				"Django outer duration counters are missing, invalid, or do not match completed responses")
		}
	}
	return result, nil
}

type djangoScrape struct {
	mode                    string
	requests                map[string]httpRequestTuple
	totals                  pythonRequestTotals
	process                 pythonProcess
	signature               prometheusUnlabelledValue
	duration                prometheusDuration
	badDuration, inlineView bool
}

func scrapeDjango(ctx context.Context, endpoint, mode string, client *prometheus.Client) (*djangoScrape, error) {
	if !config.ValidDjangoMetricsMode(mode) || client == nil {
		return nil, errors.New("Django requires a metrics mode (inline or separate) and a metrics client")
	}
	s := &djangoScrape{mode: mode, requests: make(map[string]httpRequestTuple)}
	if _, err := client.Scrape(ctx, endpoint, s.accept); err != nil {
		return nil, fmt.Errorf("scraping Django metrics: %w", err)
	}
	if !s.signature.valid || !validPrometheusCount(s.signature.value) {
		return nil, fmt.Errorf("%w: Django middleware counter was not observed", ErrDjangoIncompatible)
	}
	if mode == config.DjangoMetricsModeSeparate && s.inlineView {
		return nil, fmt.Errorf("%w: observed the inline Django metrics view with separate mode", ErrDjangoIncompatible)
	}
	return s, nil
}

func (s *djangoScrape) accept(sample prometheus.Sample) error {
	switch sample.Name {
	case djangoSignature:
		s.signature.accept(sample)
	case djangoViewStatuses, djangoStatuses:
		return s.acceptResponse(sample)
	case djangoDurationCount, djangoDurationSum:
		if s.mode == config.DjangoMetricsModeSeparate && !s.badDuration {
			s.badDuration = len(sample.Labels) != 0 || !s.duration.accept(sample.Name == djangoDurationCount, sample.Value)
		}
	default:
		s.process.accept(sample)
	}
	return nil
}

func (s *djangoScrape) acceptResponse(sample prometheus.Sample) error {
	status, view, hasView, hasMethod := djangoResponseLabels(sample.Labels)
	isViewTuple := sample.Name == djangoViewStatuses
	if isViewTuple && view == djangoMetricsView {
		s.inlineView = true
		return nil
	}
	if isViewTuple != (s.mode == config.DjangoMetricsModeInline) {
		return nil
	}
	code, grouped, valid := pythonStatus(status)
	validLabels := len(sample.Labels) == 1
	if isViewTuple {
		validLabels = len(sample.Labels) == 3 && hasView && hasMethod
	}
	if !validLabels || !valid || grouped || !validPrometheusCount(sample.Value) {
		return errors.New("invalid Django response tuple")
	}
	key := prometheusTupleKey(sample.Labels)
	if _, exists := s.requests[key]; exists {
		return errors.New("duplicate Django response tuple")
	}
	if len(s.requests) >= pythonMaxTuples || !s.totals.add(sample.Value, code, false) {
		return errors.New("Django response population exceeds tuple or exact count limit")
	}
	s.requests[key] = httpRequestTuple{count: sample.Value}
	return nil
}

func djangoResponseLabels(labels []prometheus.Label) (status, view string, hasView, hasMethod bool) {
	for _, label := range labels {
		switch label.Name {
		case "status":
			status = label.Value
		case "view":
			view, hasView = label.Value, true
		case "method":
			hasMethod = true
		}
	}
	return
}

func (s *djangoScrape) hasLatency() bool {
	d := s.duration
	return !s.badDuration && d.hasCount && d.hasSum && d.count == s.totals.requests && (d.count != 0 || d.sum == 0)
}

func InspectDjango(ctx context.Context, endpoint, mode string, client *prometheus.Client) (capabilities, warnings []string, err error) {
	s, err := scrapeDjango(ctx, endpoint, mode, client)
	if err != nil {
		return nil, nil, err
	}
	capabilities = []string{"metrics"}
	if len(s.requests) == 0 {
		warnings = append(warnings, "send a request to an application route to initialize Django HTTP metrics")
	} else {
		capabilities = append(capabilities, "requests", "http_4xx", "http_5xx", "http_404")
		if mode == config.DjangoMetricsModeSeparate {
			if s.hasLatency() {
				capabilities = append(capabilities, "latency")
			} else {
				warnings = append(warnings, "latency is unavailable for the selected Django response population")
			}
		}
	}
	if mode == config.DjangoMetricsModeInline && !s.inlineView {
		warnings = append(warnings, "the inline Django metrics view has not been observed yet")
	}
	if s.process.startTime(time.Now()) != nil {
		capabilities = append(capabilities, "process_start_time")
		if s.process.cpu.valid {
			capabilities = append(capabilities, "process_cpu_usage")
		}
	}
	return capabilities, warnings, nil
}
