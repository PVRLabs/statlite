package collector

// This file fetches the fixed, language-neutral StatLite Metrics JSON profile.

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

const (
	StatliteMetricsV1Schema         = "statlite-metrics/v1"
	statliteMetricsMaxResponseBytes = 1 << 20
)

type StatliteMetricsClient struct {
	url        string
	httpClient *http.Client
}

type StatliteMetricsResponse struct {
	Schema         string                 `json:"schema"`
	Status         string                 `json:"status"`
	DatabaseStatus StatliteMetricsField   `json:"database_status,omitempty"`
	StartedAt      StatliteMetricsField   `json:"started_at,omitempty"`
	Metrics        *StatliteMetricsValues `json:"metrics,omitempty"`
}

type StatliteMetricsValues struct {
	RequestsTotal               StatliteMetricsField `json:"requests_total,omitempty"`
	Responses404Total           StatliteMetricsField `json:"responses_404_total,omitempty"`
	Responses4xxTotal           StatliteMetricsField `json:"responses_4xx_total,omitempty"`
	Responses5xxTotal           StatliteMetricsField `json:"responses_5xx_total,omitempty"`
	RequestDurationSecondsTotal StatliteMetricsField `json:"request_duration_seconds_total,omitempty"`
	// RequestDurationSecondsMax remains parseable for v1 producer compatibility;
	// the normalized collector intentionally ignores it.
	RequestDurationSecondsMax StatliteMetricsField `json:"request_duration_seconds_max,omitempty"`
	ProcessCPUUsage           StatliteMetricsField `json:"process_cpu_usage,omitempty"`
	RuntimeHeapUsedBytes      StatliteMetricsField `json:"runtime_heap_used_bytes,omitempty"`
	UptimeSeconds             StatliteMetricsField `json:"uptime_seconds,omitempty"`
	HostCPUUsage              StatliteMetricsField `json:"host_cpu_usage,omitempty"`
	HostMemoryUsedBytes       StatliteMetricsField `json:"host_memory_used_bytes,omitempty"`
	HostMemoryTotalBytes      StatliteMetricsField `json:"host_memory_total_bytes,omitempty"`
	HostDiskUsedBytes         StatliteMetricsField `json:"host_disk_used_bytes,omitempty"`
	HostDiskTotalBytes        StatliteMetricsField `json:"host_disk_total_bytes,omitempty"`
}

// StatliteMetricsField preserves the JSON representation of an optional field.
// Validation belongs to the collector so one bad optional value can become a
// warning without making the otherwise usable response fail at decode time.
type StatliteMetricsField struct {
	raw json.RawMessage
}

func (f *StatliteMetricsField) UnmarshalJSON(data []byte) error {
	f.raw = append(f.raw[:0], data...)
	return nil
}

func (f StatliteMetricsField) present() bool {
	return len(f.raw) != 0
}

func NewStatliteMetricsClient(rawURL string, timeout time.Duration) (*StatliteMetricsClient, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("statlite metrics timeout must be positive")
	}

	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parsing statlite metrics URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("statlite metrics URL must use http or https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("statlite metrics URL must include a host")
	}

	return &StatliteMetricsClient{
		url: parsed.String(),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *StatliteMetricsClient) Fetch(ctx context.Context) (*StatliteMetricsResponse, error) {
	req, err := c.newRequest(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching statlite metrics: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, statliteMetricsMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading statlite metrics response: %w", err)
	}
	if len(body) > statliteMetricsMaxResponseBytes {
		return nil, fmt.Errorf("statlite metrics response exceeds %d byte limit", statliteMetricsMaxResponseBytes)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("statlite metrics returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return ParseStatliteMetricsResponse(body)
}

// ParseStatliteMetricsResponse decodes and validates the required fields of the
// StatLite Metrics wire contract. Optional metric validation remains the
// collector's responsibility so a usable response can retain partial data.
func ParseStatliteMetricsResponse(body []byte) (*StatliteMetricsResponse, error) {
	var metrics StatliteMetricsResponse
	if err := json.Unmarshal(body, &metrics); err != nil {
		return nil, fmt.Errorf("parsing statlite metrics response: %w", err)
	}
	if metrics.Schema == "" {
		return nil, fmt.Errorf("statlite metrics response missing required schema")
	}
	if metrics.Schema != StatliteMetricsV1Schema {
		return nil, fmt.Errorf("statlite metrics response has unsupported schema %q", metrics.Schema)
	}
	if strings.TrimSpace(metrics.Status) == "" {
		return nil, fmt.Errorf("statlite metrics response missing required status")
	}

	return &metrics, nil
}

func (c *StatliteMetricsClient) newRequest(ctx context.Context) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating statlite metrics request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}
