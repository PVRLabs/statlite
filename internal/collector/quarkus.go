// This file validates Quarkus Prometheus/OpenMetrics scrapes before
// Quarkus-specific normalization.
package collector

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"math/bits"
	"strconv"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

// QuarkusCollector performs the single bounded exposition scrape owned by a
// Quarkus polling cycle. Quarkus normalization is added separately.
type QuarkusCollector struct {
	targetName string
	endpoint   string
	client     *prometheus.Client
}

// quarkusHTTPValues retains fixed-size StatLite aggregates plus a bounded set
// of label fingerprints used only to match timer count and sum populations.
// Source labels and series are inspected while streaming and are not retained.
type quarkusHTTPValues struct {
	requests             float64
	durationSeconds      float64
	notFound             float64
	clientErrors         float64
	serverErrors         float64
	sawCount             bool
	sawDuration          bool
	completeCountLabels  bool
	incompleteCountLabel bool
	incompleteSumLabel   bool
	countDimensions      map[quarkusHTTPDimensionHash]int
	durationDimensions   map[quarkusHTTPDimensionHash]int
	matchingStates       int
	matchingOverflow     bool
	requestOverflow      bool
	statusOverflow       bool
	durationOverflow     bool
	countDuplicate       bool
	durationDuplicate    bool
}

type quarkusHTTPDimensionHash struct{ first, second uint64 }

// Count and sum each contribute at most the shared parser's default 10,000
// aggregation identities. The combined cap remains fixed regardless of source
// series count.
const quarkusHTTPMatchingStateLimit = 20_000

func NewQuarkusCollector(targetName, endpoint string, client *prometheus.Client) *QuarkusCollector {
	return &QuarkusCollector{targetName: targetName, endpoint: endpoint, client: client}
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

	compatible := false
	httpValues := quarkusHTTPValues{completeCountLabels: true}
	_, err := c.client.Scrape(ctx, c.endpoint, func(sample prometheus.Sample) error {
		switch sample.Name {
		case "http_server_requests_seconds_count":
			httpValues.acceptCount(sample)
			return nil
		case "http_server_requests_seconds_sum":
			httpValues.acceptDuration(sample)
			return nil
		}
		if math.IsNaN(sample.Value) || math.IsInf(sample.Value, 0) {
			return nil
		}
		switch sample.Name {
		case "process_cpu_usage", "process_start_time_seconds":
			compatible = true
		case "jvm_memory_used_bytes":
			for _, label := range sample.Labels {
				if label.Name == "area" && label.Value == "heap" {
					compatible = true
				}
			}
		}
		return nil
	})
	if err != nil {
		wrapped := fmt.Errorf("scraping Quarkus metrics: %w", err)
		result.addEvent(EventSeverityError, "metrics_fetch_failed", "", wrapped.Error())
		return result, wrapped
	}
	if !compatible {
		err := errors.New("Quarkus metrics endpoint does not expose a finite recognized runtime family")
		result.addEvent(EventSeverityError, "metrics_source_incompatible", "", err.Error())
		return result, err
	}
	httpValues.addTo(result)
	return result, nil
}

func (v *quarkusHTTPValues) acceptCount(sample prometheus.Sample) {
	method, outcome, status, ok := quarkusHTTPDimensions(sample)
	if !ok || method == "" || outcome == "" || !finiteNonnegative(sample.Value) {
		v.completeCountLabels = false
		v.incompleteCountLabel = true
		return
	}
	code, err := strconv.Atoi(status)
	if err != nil || code < 100 || code > 599 {
		v.completeCountLabels = false
		v.incompleteCountLabel = true
		return
	}
	dimension := quarkusHTTPSeriesFingerprint(sample.Labels)
	if v.countDimensions[dimension] > 0 {
		v.countDuplicate = true
		return
	}
	v.sawCount = true
	var added bool
	v.requests, added = addFinite(v.requests, sample.Value)
	if !added {
		v.requestOverflow = true
	}
	v.addMatchingDimension(&v.countDimensions, dimension)
	if code == 404 {
		v.notFound, added = addFinite(v.notFound, sample.Value)
		v.statusOverflow = v.statusOverflow || !added
	}
	if code >= 400 && code <= 499 {
		v.clientErrors, added = addFinite(v.clientErrors, sample.Value)
		v.statusOverflow = v.statusOverflow || !added
	}
	if code >= 500 {
		v.serverErrors, added = addFinite(v.serverErrors, sample.Value)
		v.statusOverflow = v.statusOverflow || !added
	}
}

func (v *quarkusHTTPValues) acceptDuration(sample prometheus.Sample) {
	method, outcome, status, ok := quarkusHTTPDimensions(sample)
	code, err := strconv.Atoi(status)
	if !ok || method == "" || outcome == "" || err != nil || code < 100 || code > 599 || !finiteNonnegative(sample.Value) {
		v.incompleteSumLabel = true
		return
	}
	dimension := quarkusHTTPSeriesFingerprint(sample.Labels)
	if v.durationDimensions[dimension] > 0 {
		v.durationDuplicate = true
		return
	}
	v.sawDuration = true
	var added bool
	v.durationSeconds, added = addFinite(v.durationSeconds, sample.Value)
	if !added {
		v.durationOverflow = true
	}
	v.addMatchingDimension(&v.durationDimensions, dimension)
}

func finiteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func addFinite(total, value float64) (float64, bool) {
	result := total + value
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return total, false
	}
	return result, true
}

func (v *quarkusHTTPValues) addMatchingDimension(groups *map[quarkusHTTPDimensionHash]int, key quarkusHTTPDimensionHash) {
	if v.matchingOverflow {
		return
	}
	if *groups == nil {
		*groups = make(map[quarkusHTTPDimensionHash]int)
	}
	if _, exists := (*groups)[key]; !exists {
		if v.matchingStates == quarkusHTTPMatchingStateLimit {
			v.matchingOverflow = true
			return
		}
		v.matchingStates++
	}
	(*groups)[key]++
}

func quarkusHTTPSeriesFingerprint(labels []prometheus.Label) quarkusHTTPDimensionHash {
	// Label order is not part of Prometheus series identity. Combine hashes of
	// every bounded name/value pair commutatively so count and sum still match
	// when exposition order differs, without retaining any raw label.
	var fingerprint quarkusHTTPDimensionHash
	for _, label := range labels {
		first := hashHTTPDimensions(fnv.New64a(), label.Name, label.Value)
		second := hashHTTPDimensions(fnv.New64(), label.Name, label.Value)
		fingerprint.first += first
		fingerprint.second += second ^ bits.RotateLeft64(first, 23)
	}
	fingerprint.first ^= uint64(len(labels)) * 0x9e3779b97f4a7c15
	fingerprint.second ^= uint64(len(labels)) * 0xc2b2ae3d27d4eb4f
	return fingerprint
}

func hashHTTPDimensions(hash interface {
	Write([]byte) (int, error)
	Sum64() uint64
}, values ...string) uint64 {
	for _, value := range values {
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write([]byte(value))
	}
	return hash.Sum64()
}

func (v *quarkusHTTPValues) durationMatchesCounts() bool {
	if v.matchingOverflow || v.requestOverflow || v.durationOverflow || v.countDuplicate || v.durationDuplicate || !v.sawCount || !v.sawDuration || len(v.countDimensions) != len(v.durationDimensions) {
		return false
	}
	for key, countSeries := range v.countDimensions {
		if v.durationDimensions[key] != countSeries {
			return false
		}
	}
	return true
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

func (v *quarkusHTTPValues) addTo(result *CollectionResult) {
	if v.sawCount && !v.requestOverflow && !v.countDuplicate && !v.matchingOverflow {
		result.addSample("http_requests_total", MetricKindCounter, v.requests, "requests")
	}
	if v.sawCount && v.completeCountLabels && !v.statusOverflow && !v.countDuplicate && !v.matchingOverflow {
		result.addSample("http_404_total", MetricKindCounter, v.notFound, "requests")
		result.addSample("http_4xx_total", MetricKindCounter, v.clientErrors, "requests")
		result.addSample("http_5xx_total", MetricKindCounter, v.serverErrors, "requests")
	}
	durationMatches := v.durationMatchesCounts()
	if durationMatches {
		result.addSample("http_request_time_total_seconds", MetricKindCounter, v.durationSeconds, "seconds")
	}
	if v.incompleteCountLabel {
		result.addEvent(EventSeverityWarning, "metric_dimension_invalid", "http_requests_total", "ignored http_server_requests_seconds_count series without valid method, outcome, and status dimensions and a finite nonnegative value; status counters are unavailable")
	}
	if v.incompleteSumLabel {
		result.addEvent(EventSeverityWarning, "metric_dimension_invalid", "http_request_time_total_seconds", "ignored http_server_requests_seconds_sum series without valid method, outcome, and status dimensions and a finite nonnegative value")
	}
	if v.sawDuration && !durationMatches && !v.requestOverflow && !v.durationOverflow && !v.countDuplicate && !v.durationDuplicate && !v.matchingOverflow {
		result.addEvent(EventSeverityWarning, "metric_series_mismatch", "http_request_time_total_seconds", "omitted HTTP request duration because its accepted dimensions do not match request count series within the bounded matching state")
	}
	if v.requestOverflow {
		result.addEvent(EventSeverityWarning, "metric_aggregate_invalid", "http_requests_total", "omitted HTTP request count because finite source values overflowed the normalized aggregate")
	}
	if v.statusOverflow {
		result.addEvent(EventSeverityWarning, "metric_aggregate_invalid", "", "omitted HTTP status counters because finite source values overflowed a normalized status aggregate")
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
