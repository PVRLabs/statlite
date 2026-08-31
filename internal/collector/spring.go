package collector

// This file maps Spring Boot Actuator responses into normalized StatLite samples.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pvrlabs/statlite/internal/prometheus"
)

type SpringMetricsSource string

const (
	SpringMetricsSourceAuto       SpringMetricsSource = "auto"
	SpringMetricsSourcePrometheus SpringMetricsSource = "prometheus"
	SpringMetricsSourceActuator   SpringMetricsSource = "actuator"
)

type SpringActuatorCollector struct {
	targetName         string
	client             *ActuatorClient
	collectHostMetrics bool
	prometheusClient   *prometheus.Client
	prometheusURL      string

	mu             sync.Mutex
	selectedSource SpringMetricsSource
}

type springPollSession struct {
	client        *ActuatorClient
	rawResults    map[string]actuatorRawResult
	failureGroups map[springFailureIdentity]*springFailureGroup
}

type springFailureIdentity struct {
	endpoint   string
	class      actuatorFailureClass
	statusCode int
}

type springFailureGroup struct {
	eventIndex   int
	affectedKeys map[string]struct{}
	failure      *actuatorFailure
}

func newSpringPollSession(client *ActuatorClient) *springPollSession {
	return &springPollSession{
		client:        client,
		rawResults:    make(map[string]actuatorRawResult),
		failureGroups: make(map[springFailureIdentity]*springFailureGroup),
	}
}

func (s *springPollSession) fetchRaw(ctx context.Context, endpointPath string, query url.Values) actuatorRawResult {
	key := s.client.effectiveURL(endpointPath, query)
	if raw, ok := s.rawResults[key]; ok {
		return raw
	}
	if err := ctx.Err(); err != nil {
		requestErr := &url.Error{Op: "Get", URL: key, Err: err}
		message := fmt.Sprintf("fetching actuator %s: %v", endpointPath, requestErr)
		raw := actuatorRawResult{
			effectiveURL: key,
			err:          newActuatorFailure(actuatorFailureTransport, key, 0, requestErr.Error(), message, requestErr),
		}
		s.rawResults[key] = raw
		return raw
	}
	raw := s.client.fetchRaw(ctx, endpointPath, query)
	s.rawResults[key] = raw
	return raw
}

func (s *springPollSession) addMetricFetchFailure(result *CollectionResult, metricKey string, err error, message string) {
	var failure *actuatorFailure
	if !errors.As(err, &failure) {
		result.addEvent(EventSeverityWarning, "metric_fetch_failed", metricKey, message)
		return
	}

	identity := springFailureIdentity{
		endpoint:   failure.endpoint,
		class:      failure.class,
		statusCode: failure.statusCode,
	}
	group, ok := s.failureGroups[identity]
	if !ok {
		result.addEvent(EventSeverityWarning, "metric_fetch_failed", metricKey, message)
		s.failureGroups[identity] = &springFailureGroup{
			eventIndex:   len(result.Events) - 1,
			affectedKeys: map[string]struct{}{metricKey: {}},
			failure:      failure,
		}
		return
	}

	group.affectedKeys[metricKey] = struct{}{}
	if len(group.affectedKeys) < 2 {
		return
	}
	keys := make([]string, 0, len(group.affectedKeys))
	for key := range group.affectedKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result.Events[group.eventIndex] = CollectorEvent{
		Severity: EventSeverityWarning,
		Type:     "metric_fetch_failed",
		Message:  formatSpringFailure(group.failure, keys),
	}
}

func formatSpringFailure(failure *actuatorFailure, affectedKeys []string) string {
	status := ""
	if failure.statusCode != 0 {
		status = fmt.Sprintf(" returned HTTP %d", failure.statusCode)
	}
	return fmt.Sprintf(
		"effective endpoint %s%s: %s; affected metrics: %s",
		failure.endpoint,
		status,
		failure.cause,
		strings.Join(affectedKeys, ", "),
	)
}

func (s *springPollSession) FetchHealth(ctx context.Context) (*HealthResponse, error) {
	return s.client.decodeHealth(s.fetchRaw(ctx, "health", nil))
}

func (s *springPollSession) FetchMetric(ctx context.Context, name string, tags []string) (*MetricResponse, error) {
	query, err := metricQuery(name, tags)
	if err != nil {
		return nil, err
	}
	endpointPath := path.Join("metrics", name)
	return s.client.decodeMetric(endpointPath, s.fetchRaw(ctx, endpointPath, query))
}

func NewSpringActuatorCollector(targetName string, client *ActuatorClient, collectHostMetrics bool) *SpringActuatorCollector {
	return &SpringActuatorCollector{
		targetName:         targetName,
		client:             client,
		collectHostMetrics: collectHostMetrics,
		selectedSource:     SpringMetricsSourceActuator,
	}
}

func NewSpringCollector(targetName string, client *ActuatorClient, prometheusClient *prometheus.Client, prometheusURL string, source SpringMetricsSource, collectHostMetrics bool) (*SpringActuatorCollector, error) {
	if source != SpringMetricsSourceAuto && source != SpringMetricsSourcePrometheus && source != SpringMetricsSourceActuator {
		return nil, fmt.Errorf("unsupported Spring metrics source %q", source)
	}
	collector := &SpringActuatorCollector{
		targetName: targetName, client: client, collectHostMetrics: collectHostMetrics,
		prometheusClient: prometheusClient, prometheusURL: prometheusURL,
	}
	if source != SpringMetricsSourceAuto {
		collector.selectedSource = source
	}
	return collector, nil
}

func (c *SpringActuatorCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	started := time.Now().UTC()
	result := &CollectionResult{
		TargetName:    c.targetName,
		PollStartedAt: started,
	}
	defer func() {
		result.PollFinishedAt = time.Now().UTC()
	}()

	if c.client == nil {
		err := fmt.Errorf("actuator client is not configured")
		result.addEvent(EventSeverityError, "collector_not_configured", "", err.Error())
		return result, err
	}

	session := newSpringPollSession(c.client)
	health, err := session.FetchHealth(ctx)
	if err != nil {
		result.addEvent(EventSeverityError, "health_fetch_failed", "", err.Error())
		return result, fmt.Errorf("fetching health: %w", err)
	}
	result.HealthStatus = health.Status
	result.DBHealthStatus = health.DBStatus()

	c.collectMetrics(ctx, session, result)

	return result, nil
}

func (c *SpringActuatorCollector) collectActuatorMetrics(ctx context.Context, session *springPollSession, result *CollectionResult) {
	c.collectHTTP(ctx, session, result)
	c.collectGauge(ctx, session, result, "jvm_heap_used_bytes", "jvm.memory.used", []string{"area:heap"}, "VALUE", "bytes")
	c.collectGauge(ctx, session, result, "process_cpu_usage", "process.cpu.usage", nil, "VALUE", "ratio")
	if c.collectHostMetrics {
		c.collectHostResources(ctx, session, result)
	}
	c.collectProcessStartTime(ctx, session, result)
}

func (c *SpringActuatorCollector) collectHostResources(ctx context.Context, session *springPollSession, result *CollectionResult) {
	if cpu, ok := c.fetchGauge(ctx, session, result, "host_cpu_usage", "system.cpu.usage"); ok {
		if !finiteInRange(cpu, 0, 1) {
			result.addEvent(EventSeverityWarning, "metric_invalid", "host_cpu_usage", fmt.Sprintf("system.cpu.usage value %v must be between 0 and 1", cpu))
		} else {
			result.addSample("host_cpu_usage", MetricKindGauge, cpu, "ratio")
		}
	}

	free, freeOK := c.fetchGauge(ctx, session, result, "host_disk_used_bytes", "disk.free")
	total, totalOK := c.fetchGauge(ctx, session, result, "host_disk_total_bytes", "disk.total")
	if !freeOK || !totalOK {
		return
	}
	if !finiteInRange(free, 0, math.MaxFloat64) || !finiteInRange(total, 0, math.MaxFloat64) || total == 0 || free > total {
		result.addEvent(EventSeverityWarning, "metric_invalid", "host_disk_used_bytes", fmt.Sprintf("disk metrics require 0 <= free <= total and total > 0; free=%v total=%v", free, total))
		return
	}

	result.addSample("host_disk_used_bytes", MetricKindGauge, total-free, "bytes")
	result.addSample("host_disk_total_bytes", MetricKindGauge, total, "bytes")
}

func (c *SpringActuatorCollector) fetchGauge(ctx context.Context, session *springPollSession, result *CollectionResult, key, actuatorName string) (float64, bool) {
	metric, err := session.FetchMetric(ctx, actuatorName, nil)
	if err != nil {
		session.addMetricFetchFailure(result, key, err, err.Error())
		return 0, false
	}
	value, ok := metricMeasurement(metric, "VALUE")
	if !ok {
		result.addEvent(EventSeverityWarning, "metric_measurement_missing", key, fmt.Sprintf("%s missing VALUE measurement", actuatorName))
		return 0, false
	}
	return value, true
}

func finiteInRange(value, min, max float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= min && value <= max
}

func (c *SpringActuatorCollector) collectHTTP(ctx context.Context, session *springPollSession, result *CollectionResult) {
	metric, err := session.FetchMetric(ctx, "http.server.requests", nil)
	if err != nil {
		session.addMetricFetchFailure(result, "http_requests_total", err, err.Error())
		return
	}

	if value, ok := metricMeasurement(metric, "COUNT"); ok {
		result.addSample("http_requests_total", MetricKindCounter, value, "requests")
	} else {
		result.addEvent(EventSeverityWarning, "metric_measurement_missing", "http_requests_total", "http.server.requests missing COUNT measurement")
	}
	if value, ok := metricMeasurement(metric, "TOTAL_TIME"); ok {
		result.addSample("http_request_time_total_seconds", MetricKindCounter, value, "seconds")
	} else {
		result.addEvent(EventSeverityWarning, "metric_measurement_missing", "http_request_time_total_seconds", "http.server.requests missing TOTAL_TIME measurement")
	}
	statuses := metricTagValues(metric, "status")
	if len(statuses) == 0 {
		result.addEvent(EventSeverityWarning, "metric_tag_missing", "http_4xx_total", "http.server.requests does not expose status tags")
		return
	}

	c.collectHTTPStatusTotal(ctx, session, result, "http_404_total", filterStatusExact(statuses, "404"))
	c.collectHTTPStatusTotal(ctx, session, result, "http_4xx_total", filterStatusRange(statuses, 400, 499))
	c.collectHTTPStatusTotal(ctx, session, result, "http_5xx_total", filterStatusRange(statuses, 500, 599))
}

func (c *SpringActuatorCollector) collectHTTPStatusTotal(ctx context.Context, session *springPollSession, result *CollectionResult, key string, statuses []string) {
	var total float64
	var sawStatus bool

	for _, status := range statuses {
		metric, err := session.FetchMetric(ctx, "http.server.requests", []string{"status:" + status})
		if err != nil {
			message := fmt.Sprintf("http.server.requests status %s: %v", status, err)
			session.addMetricFetchFailure(result, key, err, message)
			continue
		}
		value, ok := metricMeasurement(metric, "COUNT")
		if !ok {
			result.addEvent(EventSeverityWarning, "metric_measurement_missing", key, fmt.Sprintf("http.server.requests status %s missing COUNT measurement", status))
			continue
		}
		total += value
		sawStatus = true
	}

	if sawStatus {
		result.addSample(key, MetricKindCounter, total, "requests")
		return
	}
	if len(statuses) == 0 {
		result.addSample(key, MetricKindCounter, 0, "requests")
	}
}

func (c *SpringActuatorCollector) collectGauge(ctx context.Context, session *springPollSession, result *CollectionResult, key, actuatorName string, tags []string, statistic, unit string) {
	metric, err := session.FetchMetric(ctx, actuatorName, tags)
	if err != nil {
		session.addMetricFetchFailure(result, key, err, err.Error())
		return
	}

	value, ok := metricMeasurement(metric, statistic)
	if !ok {
		result.addEvent(EventSeverityWarning, "metric_measurement_missing", key, fmt.Sprintf("%s missing %s measurement", actuatorName, statistic))
		return
	}
	result.addSample(key, MetricKindGauge, value, unit)
}

func (c *SpringActuatorCollector) collectProcessStartTime(ctx context.Context, session *springPollSession, result *CollectionResult) {
	metric, err := session.FetchMetric(ctx, "process.start.time", nil)
	if err != nil {
		session.addMetricFetchFailure(result, "process_start_time", err, err.Error())
		return
	}

	value, ok := metricMeasurement(metric, "VALUE")
	if !ok {
		result.addEvent(EventSeverityWarning, "metric_measurement_missing", "process_start_time", "process.start.time missing VALUE measurement")
		return
	}

	result.addSample("process_start_time", MetricKindGauge, value, "unix_seconds")
	startTime := unixSeconds(value)
	result.ProcessStartTime = &startTime
}

func metricMeasurement(metric *MetricResponse, statistic string) (float64, bool) {
	for _, measurement := range metric.Measurements {
		if strings.EqualFold(measurement.Statistic, statistic) {
			return measurement.Value, true
		}
	}
	return 0, false
}

func metricTagValues(metric *MetricResponse, tag string) []string {
	for _, availableTag := range metric.AvailableTags {
		if strings.EqualFold(availableTag.Tag, tag) {
			return availableTag.Values
		}
	}
	return nil
}

func filterStatusRange(statuses []string, min, max int) []string {
	var filtered []string
	for _, status := range statuses {
		code, err := strconv.Atoi(status)
		if err != nil {
			continue
		}
		if code >= min && code <= max {
			filtered = append(filtered, status)
		}
	}
	return filtered
}

func filterStatusExact(statuses []string, exact string) []string {
	for _, status := range statuses {
		if status == exact {
			return []string{status}
		}
	}
	return nil
}

func unixSeconds(value float64) time.Time {
	seconds := int64(value)
	nanos := int64((value - float64(seconds)) * 1_000_000_000)
	return time.Unix(seconds, nanos).UTC()
}
