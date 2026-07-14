package collector

// This file defines the normalized collection result shared beyond collector adapters.

import "time"

type MetricKind string

const (
	MetricKindCounter MetricKind = "counter"
	MetricKindGauge   MetricKind = "gauge"
)

type EventSeverity string

const (
	EventSeverityWarning EventSeverity = "warning"
	EventSeverityError   EventSeverity = "error"
)

type CollectionResult struct {
	TargetName       string           `json:"target_name"`
	PollStartedAt    time.Time        `json:"poll_started_at"`
	PollFinishedAt   time.Time        `json:"poll_finished_at"`
	HealthStatus     string           `json:"health_status,omitempty"`
	DBHealthStatus   string           `json:"db_health_status,omitempty"`
	ProcessStartTime *time.Time       `json:"process_start_time,omitempty"`
	Samples          []MetricSample   `json:"samples"`
	Events           []CollectorEvent `json:"events"`
}

type MetricSample struct {
	Key   string     `json:"key"`
	Kind  MetricKind `json:"kind"`
	Value float64    `json:"value"`
	Unit  string     `json:"unit,omitempty"`
}

type CollectorEvent struct {
	Severity  EventSeverity `json:"severity"`
	Type      string        `json:"type"`
	Message   string        `json:"message"`
	MetricKey string        `json:"metric_key,omitempty"`
}

func (r *CollectionResult) addSample(key string, kind MetricKind, value float64, unit string) {
	r.Samples = append(r.Samples, MetricSample{
		Key:   key,
		Kind:  kind,
		Value: value,
		Unit:  unit,
	})
}

func (r *CollectionResult) addEvent(severity EventSeverity, eventType, metricKey, message string) {
	r.Events = append(r.Events, CollectorEvent{
		Severity:  severity,
		Type:      eventType,
		MetricKey: metricKey,
		Message:   message,
	})
}
