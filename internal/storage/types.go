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
}

type SeriesPoint struct {
	PollID                int64     `json:"poll_id"`
	Timestamp             time.Time `json:"timestamp"`
	AppRunID              *int64    `json:"app_run_id"`
	Requests              *float64  `json:"requests"`
	HTTP404               *float64  `json:"http_404"`
	HTTP4xx               *float64  `json:"http_4xx"`
	HTTP5xx               *float64  `json:"http_5xx"`
	AverageLatencySeconds *float64  `json:"average_latency_seconds"`
	HeapUsedBytes         *float64  `json:"heap_used_bytes"`
	ProcessCPUUsage       *float64  `json:"process_cpu_usage"`
}

type Event struct {
	PollID    int64     `json:"poll_id"`
	Timestamp time.Time `json:"timestamp"`
	Severity  string    `json:"severity"`
	Type      string    `json:"type"`
	MetricKey string    `json:"metric_key,omitempty"`
	Message   string    `json:"message"`
}
