package collector

// This file collects another StatLite instance through its healthz endpoint.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type StatliteHealthzClient struct {
	url        string
	httpClient *http.Client
}

type StatliteHealthzResponse struct {
	Status    string                 `json:"status"`
	Version   string                 `json:"version,omitempty"`
	Timestamp string                 `json:"timestamp,omitempty"`
	Statlite  StatliteHealthzMetrics `json:"statlite"`
	Raw       json.RawMessage        `json:"raw,omitempty"`
}

type StatliteHealthzMetrics struct {
	UptimeSeconds    *float64               `json:"uptime_seconds,omitempty"`
	ProcessStartTime string                 `json:"process_start_time,omitempty"`
	HTTP             StatliteHealthzHTTP    `json:"http,omitempty"`
	Runtime          StatliteHealthzRuntime `json:"runtime,omitempty"`
	Polling          StatliteHealthzPolling `json:"polling,omitempty"`
	Storage          StatliteHealthzStorage `json:"storage,omitempty"`
}

type StatliteHealthzHTTP struct {
	RequestsTotal    *float64 `json:"requests_total,omitempty"`
	NotFoundTotal    *float64 `json:"not_found_total,omitempty"`
	ServerErrorTotal *float64 `json:"server_error_total,omitempty"`
}

type StatliteHealthzRuntime struct {
	MemoryAllocBytes *float64 `json:"memory_alloc_bytes,omitempty"`
	MemorySysBytes   *float64 `json:"memory_sys_bytes,omitempty"`
	Goroutines       *float64 `json:"goroutines,omitempty"`
	ProcessCPUUsage  *float64 `json:"process_cpu_usage,omitempty"`
}

type StatliteHealthzStorage struct {
	Status           string `json:"status,omitempty"`
	LastStoredPollID int64  `json:"last_stored_poll_id,omitempty"`
}

type StatliteHealthzPolling struct {
	ConsecutiveFailures  *int    `json:"consecutive_failures,omitempty"`
	LastPollAt           *string `json:"last_poll_at,omitempty"`
	LastSuccessfulPollAt *string `json:"last_successful_poll_at,omitempty"`
	LastFailedPollAt     *string `json:"last_failed_poll_at,omitempty"`
}

type StatliteHealthzCollector struct {
	targetName string
	client     *StatliteHealthzClient
}

func NewStatliteHealthzClient(rawURL string, timeout time.Duration) (*StatliteHealthzClient, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("statlite healthz timeout must be positive")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parsing statlite healthz URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("statlite healthz URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("statlite healthz URL must include a host")
	}
	return &StatliteHealthzClient{
		url: parsed.String(),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func NewStatliteHealthzCollector(targetName string, client *StatliteHealthzClient) *StatliteHealthzCollector {
	return &StatliteHealthzCollector{
		targetName: targetName,
		client:     client,
	}
}

func (c *StatliteHealthzCollector) Collect(ctx context.Context) (*CollectionResult, error) {
	started := time.Now().UTC()
	result := &CollectionResult{
		TargetName:    c.targetName,
		PollStartedAt: started,
	}
	defer func() {
		result.PollFinishedAt = time.Now().UTC()
	}()

	if c.client == nil {
		err := fmt.Errorf("statlite healthz client is not configured")
		result.addEvent(EventSeverityError, "collector_not_configured", "", err.Error())
		return result, err
	}

	healthz, err := c.client.Fetch(ctx)
	if err != nil {
		result.addEvent(EventSeverityError, "healthz_fetch_failed", "", err.Error())
		return result, fmt.Errorf("fetching statlite healthz: %w", err)
	}
	result.HealthStatus = healthz.Status
	if healthz.Statlite.ProcessStartTime != "" {
		processStartTime, err := time.Parse(time.RFC3339Nano, healthz.Statlite.ProcessStartTime)
		if err != nil {
			result.addEvent(EventSeverityWarning, "process_start_time_invalid", "process_start_time", fmt.Sprintf("statlite healthz process_start_time must be RFC3339: %v", err))
		} else {
			processStartTime = processStartTime.UTC()
			result.ProcessStartTime = &processStartTime
			result.addSample("process_start_time", MetricKindGauge, float64(processStartTime.UnixNano())/1_000_000_000, "unix_seconds")
		}
	} else {
		result.addEvent(EventSeverityWarning, "process_start_time_missing", "process_start_time", "statlite healthz missing process_start_time")
	}
	if healthz.Statlite.Storage.Status != "" {
		result.DBHealthStatus = healthz.Statlite.Storage.Status
	} else {
		result.addEvent(EventSeverityWarning, "db_health_missing", "", "statlite healthz missing storage status")
	}

	addOptionalSample(result, "http_requests_total", MetricKindCounter, healthz.Statlite.HTTP.RequestsTotal, "requests")
	addOptionalSample(result, "http_404_total", MetricKindCounter, healthz.Statlite.HTTP.NotFoundTotal, "requests")
	addOptionalSample(result, "http_5xx_total", MetricKindCounter, healthz.Statlite.HTTP.ServerErrorTotal, "requests")
	addOptionalSample(result, "jvm_heap_used_bytes", MetricKindGauge, healthz.Statlite.Runtime.MemoryAllocBytes, "bytes")
	addOptionalSample(result, "process_uptime", MetricKindGauge, healthz.Statlite.UptimeSeconds, "seconds")
	addOptionalSample(result, "process_cpu_usage", MetricKindGauge, healthz.Statlite.Runtime.ProcessCPUUsage, "ratio")
	addOptionalSample(result, "statlite_memory_sys_bytes", MetricKindGauge, healthz.Statlite.Runtime.MemorySysBytes, "bytes")
	addOptionalSample(result, "statlite_goroutines", MetricKindGauge, healthz.Statlite.Runtime.Goroutines, "goroutines")

	return result, nil
}

func (c *StatliteHealthzClient) Fetch(ctx context.Context) (*StatliteHealthzResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating statlite healthz request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching statlite healthz: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("reading statlite healthz response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("statlite healthz returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var healthz StatliteHealthzResponse
	if err := json.Unmarshal(body, &healthz); err != nil {
		return nil, fmt.Errorf("parsing statlite healthz response: %w", err)
	}
	healthz.Raw = append(healthz.Raw[:0], body...)
	return &healthz, nil
}

func addOptionalSample(result *CollectionResult, key string, kind MetricKind, value *float64, unit string) {
	if value == nil {
		result.addEvent(EventSeverityWarning, "metric_missing", key, fmt.Sprintf("statlite healthz missing %s", key))
		return
	}
	result.addSample(key, kind, *value, unit)
}
