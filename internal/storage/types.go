package storage

// This file defines storage DTOs returned to server and dashboard callers.

import (
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

type Snapshot struct {
	PollID       int64                      `json:"poll_id"`
	TargetID     int64                      `json:"target_id"`
	AppRunID     *int64                     `json:"app_run_id,omitempty"`
	Status       string                     `json:"status"`
	ErrorSummary string                     `json:"error_summary,omitempty"`
	Result       collector.CollectionResult `json:"result"`
}

type Series struct {
	Start  time.Time     `json:"start"`
	End    time.Time     `json:"end"`
	Points []SeriesPoint `json:"points"`
	// LatestPoint preserves the newest raw point when Points are aggregated so
	// dashboard readiness does not depend on lossy bucket identity.
	LatestPoint     *SeriesPoint     `json:"latest_point,omitempty"`
	CurrentHostDisk *HostDiskCurrent `json:"current_host_disk,omitempty"`
}

// HostDiskCurrent is one complete, unaggregated disk observation for a series.
// It is kept separate from downsampled chart values so a current-value display
// never combines values from different polls.
type HostDiskCurrent struct {
	UsedBytes  float64 `json:"used_bytes"`
	TotalBytes float64 `json:"total_bytes"`
	Usage      float64 `json:"usage"`
}

type SeriesPoint struct {
	PollID                int64     `json:"poll_id,omitempty"`
	Timestamp             time.Time `json:"timestamp"`
	AppRunID              *int64    `json:"app_run_id,omitempty"`
	Requests              *float64  `json:"requests"`
	HTTP404               *float64  `json:"http_404"`
	HTTP4xx               *float64  `json:"http_4xx"`
	HTTP5xx               *float64  `json:"http_5xx"`
	AverageLatencySeconds *float64  `json:"average_latency_seconds"`
	HeapUsedBytes         *float64  `json:"heap_used_bytes"`
	ProcessCPUUsage       *float64  `json:"process_cpu_usage"`
	HostCPUUsage          *float64  `json:"host_cpu_usage"`
	HostMemoryUsedBytes   *float64  `json:"host_memory_used_bytes"`
	HostMemoryTotalBytes  *float64  `json:"host_memory_total_bytes"`
	HostMemoryUsage       *float64  `json:"host_memory_usage"`
	HostDiskUsedBytes     *float64  `json:"host_disk_used_bytes"`
	HostDiskTotalBytes    *float64  `json:"host_disk_total_bytes"`
	HostDiskUsage         *float64  `json:"host_disk_usage"`
}

type Event struct {
	PollID    int64     `json:"poll_id"`
	Timestamp time.Time `json:"timestamp"`
	Severity  string    `json:"severity"`
	Type      string    `json:"type"`
	MetricKey string    `json:"metric_key,omitempty"`
	Message   string    `json:"message"`
}
