package collector

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

const CaddyMetricsSource = "caddy"
const caddyMaxTuples = 256
const caddyMaxCount = 1<<53 - 1

// CaddyCollector owns bounded, in-memory continuity state for one exact host.
// Source labels never leave the adapter. Calls are serialized through the scrape
// so that a failed observation cannot race a successful baseline replacement.
type CaddyCollector struct {
	targetName, endpoint, host string
	client                     *prometheus.Client
	mu                         sync.Mutex
	previous                   map[string]caddyTuple
	server                     string
}

type caddyTuple struct {
	count, sum       float64
	hasCount, hasSum bool
	code             int
}

func NewCaddyCollector(targetName, endpoint, host string, client *prometheus.Client) *CaddyCollector {
	return &CaddyCollector{targetName: targetName, endpoint: endpoint, host: host, client: client}
}

func (c *CaddyCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := &CollectionResult{TargetName: c.targetName, MetricsSource: CaddyMetricsSource, PollStartedAt: time.Now().UTC()}
	defer func() { result.PollFinishedAt = time.Now().UTC() }()
	fail := func(kind string, err error) (*CollectionResult, error) {
		c.previous, c.server = nil, ""
		result.addEvent(EventSeverityError, kind, "", err.Error())
		return result, err
	}
	if c.client == nil || c.endpoint == "" || c.host == "" {
		return fail("collector_not_configured", fmt.Errorf("Caddy metrics URL, host, and client are required"))
	}
	population := caddyPopulation{host: c.host, tuples: make(map[string]caddyTuple)}
	_, err := c.client.Scrape(ctx, c.endpoint, population.accept)
	if err != nil {
		return fail("metrics_fetch_failed", fmt.Errorf("scraping Caddy metrics: %w", err))
	}
	if len(population.tuples) == 0 {
		return fail("caddy_http_not_observed", fmt.Errorf("HTTP metrics not yet observed for Caddy host %q; generate traffic and check host/metrics configuration", c.host))
	}
	totals, err := population.totals()
	if err != nil {
		return fail("caddy_population_invalid", err)
	}
	result.CounterContinuityBreak = c.previous == nil || c.server != population.server
	for key, old := range c.previous {
		current, ok := population.tuples[key]
		if !ok || current.count < old.count || current.sum < old.sum {
			result.CounterContinuityBreak = true
		}
	}
	for i, key := range []string{"http_requests_total", "http_404_total", "http_4xx_total", "http_5xx_total", "http_request_time_total_seconds"} {
		unit := "requests"
		if i == 4 {
			unit = "seconds"
		}
		result.addSample(key, MetricKindCounter, totals[i], unit)
	}
	c.previous, c.server = population.tuples, population.server
	return result, nil
}

type caddyPopulation struct {
	host, server string
	tuples       map[string]caddyTuple
}

func (p *caddyPopulation) accept(sample prometheus.Sample) error {
	isCount := sample.Name == "caddy_http_request_duration_seconds_count"
	if !isCount && sample.Name != "caddy_http_request_duration_seconds_sum" {
		return nil
	}
	labels := make(map[string]string, len(sample.Labels))
	for _, label := range sample.Labels {
		labels[label.Name] = label.Value
	}
	// Known other populations are irrelevant. Missing selector labels cannot be
	// safely assigned to another population and must not hide a partial scrape.
	if host, ok := labels["host"]; ok && host != p.host {
		return nil
	}
	if handler, ok := labels["handler"]; ok && handler != "subroute" {
		return nil
	}
	for _, name := range []string{"host", "handler", "server", "method", "code"} {
		if labels[name] == "" {
			return fmt.Errorf("Caddy HTTP population requires nonempty %s label", name)
		}
	}
	code, err := strconv.Atoi(labels["code"])
	if err != nil || len(labels["code"]) != 3 || code < 100 || code > 599 {
		return fmt.Errorf("Caddy HTTP population has invalid status code")
	}
	if p.server != "" && p.server != labels["server"] {
		return fmt.Errorf("ambiguous Caddy HTTP population: host %q matches multiple subroute servers", p.host)
	}
	p.server = labels["server"]
	if !finiteNonnegative(sample.Value) || (isCount && (math.Trunc(sample.Value) != sample.Value || sample.Value > caddyMaxCount)) {
		return fmt.Errorf("Caddy HTTP population has invalid %s value", sample.Name)
	}
	// Quoted components preserve the full tuple, including
	// unknown labels, without hash collisions or dependence on exposition order.
	ordered := slices.Clone(sample.Labels)
	slices.SortFunc(ordered, func(a, b prometheus.Label) int { return strings.Compare(a.Name, b.Name) })
	var key strings.Builder
	for _, label := range ordered {
		key.WriteString(strconv.Quote(label.Name))
		key.WriteString(strconv.Quote(label.Value))
	}
	identity := key.String()
	tuple, exists := p.tuples[identity]
	if !exists && len(p.tuples) >= caddyMaxTuples {
		return fmt.Errorf("Caddy HTTP population exceeds %d tuples", caddyMaxTuples)
	}
	tuple.code = code
	if isCount {
		if tuple.hasCount {
			return fmt.Errorf("duplicate Caddy HTTP count tuple")
		}
		tuple.count, tuple.hasCount = sample.Value, true
	} else {
		if tuple.hasSum {
			return fmt.Errorf("duplicate Caddy HTTP duration tuple")
		}
		tuple.sum, tuple.hasSum = sample.Value, true
	}
	p.tuples[identity] = tuple
	return nil
}

func (p *caddyPopulation) totals() ([5]float64, error) {
	var totals [5]float64
	// Stable addition avoids artificial duration decreases from map iteration.
	keys := make([]string, 0, len(p.tuples))
	for key := range p.tuples {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		tuple := p.tuples[key]
		if !tuple.hasCount || !tuple.hasSum {
			return totals, fmt.Errorf("Caddy HTTP count and duration label tuples do not match")
		}
		if tuple.count > caddyMaxCount-totals[0] {
			return totals, fmt.Errorf("Caddy HTTP aggregate count exceeds exact integer range")
		}
		totals[0] += tuple.count
		if tuple.code == 404 {
			totals[1] += tuple.count
		}
		if tuple.code >= 400 && tuple.code <= 499 {
			totals[2] += tuple.count
		}
		if tuple.code >= 500 {
			totals[3] += tuple.count
		}
		totals[4] += tuple.sum
		if !finiteNonnegative(totals[4]) {
			return totals, fmt.Errorf("Caddy HTTP aggregate duration overflows")
		}
	}
	return totals, nil
}
