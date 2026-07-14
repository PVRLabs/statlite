package monitor

// This file runs polling cycles and exposes cached target status and query data.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/storage"
)

type Collector interface {
	Collect(context.Context) (*collector.CollectionResult, error)
}

type Monitor struct {
	targetName string
	collector  Collector
	store      *storage.Store
	interval   time.Duration

	pollMu sync.Mutex

	statusMu sync.RWMutex
	status   Status
	latest   *storage.Snapshot
	previous *storage.Snapshot
}

type Status struct {
	LastPollAt                 *time.Time `json:"last_poll_at,omitempty"`
	LastSuccessfulPollAt       *time.Time `json:"last_successful_poll_at,omitempty"`
	LastFailedPollAt           *time.Time `json:"last_failed_poll_at,omitempty"`
	ConsecutivePollFailures    int        `json:"consecutive_poll_failures"`
	LastPollErrorSummary       string     `json:"last_poll_error_summary,omitempty"`
	LastStoredPollID           int64      `json:"last_stored_poll_id,omitempty"`
	LastSuccessfulStoredPollID int64      `json:"last_successful_stored_poll_id,omitempty"`
}

func New(targetName string, collector Collector, store *storage.Store, interval time.Duration) (*Monitor, error) {
	if strings.TrimSpace(targetName) == "" {
		return nil, fmt.Errorf("target name is required")
	}
	if collector == nil {
		return nil, fmt.Errorf("collector is required")
	}
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if interval <= 0 {
		return nil, fmt.Errorf("polling interval must be positive")
	}
	return &Monitor{
		targetName: targetName,
		collector:  collector,
		store:      store,
		interval:   interval,
	}, nil
}

func (m *Monitor) Start(ctx context.Context) {
	go m.loop(ctx)
}

func (m *Monitor) TargetName() string {
	return m.targetName
}

func (m *Monitor) PollNow(ctx context.Context) (*storage.Snapshot, error) {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()

	if m.previous == nil {
		previous, err := m.store.LatestSuccessfulSnapshot(ctx, m.targetName)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			m.previous = previous
		}
	}

	result, collectErr := m.collector.Collect(ctx)
	if result == nil {
		now := time.Now().UTC()
		result = &collector.CollectionResult{
			TargetName:     m.targetName,
			PollStartedAt:  now,
			PollFinishedAt: now,
			Events:         []collector.CollectorEvent{{Severity: collector.EventSeverityError, Type: "collector_failed", Message: "collector returned no result"}},
		}
	}

	var appRunID *int64
	if collectErr == nil && !hasErrorEvent(result.Events) {
		id, restartDetected, reason, err := m.detectAppRun(ctx, result)
		if err != nil {
			m.recordFailure(result.PollFinishedAt, 0, fmt.Sprintf("detect app run: %v", err), nil)
			return nil, err
		}
		appRunID = &id
		if restartDetected {
			result.Events = append(result.Events, collector.CollectorEvent{
				Severity: collector.EventSeverityWarning,
				Type:     "restart_detected",
				Message:  reason,
			})
		}
	}

	pollID, saveErr := m.store.SaveCollectionResultWithAppRun(ctx, result, appRunID)
	if saveErr != nil {
		m.recordFailure(result.PollFinishedAt, 0, fmt.Sprintf("store poll: %v", saveErr), nil)
		return nil, saveErr
	}

	snapshot, err := m.store.LatestSnapshot(ctx, m.targetName)
	if err != nil {
		m.recordFailure(result.PollFinishedAt, pollID, fmt.Sprintf("load stored poll: %v", err), nil)
		return nil, err
	}

	if collectErr != nil || hasErrorEvent(result.Events) {
		summary := snapshot.ErrorSummary
		if collectErr != nil && summary == "" {
			summary = collectErr.Error()
		}
		m.recordFailure(result.PollFinishedAt, pollID, summary, snapshot)
		return snapshot, collectErr
	}

	m.recordSuccess(result.PollFinishedAt, pollID, snapshot)
	return snapshot, nil
}

func (m *Monitor) LatestSnapshot() *storage.Snapshot {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	return m.latest
}

func (m *Monitor) Status() Status {
	m.statusMu.RLock()
	defer m.statusMu.RUnlock()
	return m.status
}

func (m *Monitor) StorageHealthy(ctx context.Context) bool {
	return m.store.Ping(ctx) == nil
}

func (m *Monitor) Series(ctx context.Context, start, end time.Time) (*storage.Series, error) {
	return m.store.Series(ctx, m.targetName, start, end)
}

func (m *Monitor) Events(ctx context.Context, start, end time.Time) ([]storage.Event, error) {
	return m.store.Events(ctx, m.targetName, start, end)
}

func (m *Monitor) loop(ctx context.Context) {
	m.PollNow(ctx)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.PollNow(ctx)
		}
	}
}

func (m *Monitor) detectAppRun(ctx context.Context, result *collector.CollectionResult) (int64, bool, string, error) {
	if m.previous == nil || m.previous.AppRunID == nil {
		id, err := m.store.EnsureAppRun(ctx, result.TargetName, result.ProcessStartTime, result.PollStartedAt)
		return id, false, "", err
	}

	if m.processStartChanged(result) {
		id, err := m.store.EnsureAppRun(ctx, result.TargetName, result.ProcessStartTime, result.PollStartedAt)
		return id, true, "process.start.time changed", err
	}
	if uptimeDecreased(m.previous.Result.Samples, result.Samples) {
		id, err := m.store.EnsureAppRun(ctx, result.TargetName, result.ProcessStartTime, result.PollStartedAt)
		return id, true, "process uptime decreased", err
	}

	coreDrops := decreasedCoreCounters(m.previous.Result.Samples, result.Samples)
	if len(coreDrops) >= 2 {
		id, err := m.store.EnsureAppRun(ctx, result.TargetName, result.ProcessStartTime, result.PollStartedAt)
		return id, true, "core cumulative counters decreased", err
	}
	if m.Status().ConsecutivePollFailures > 0 && len(coreDrops) > 0 {
		id, err := m.store.EnsureAppRun(ctx, result.TargetName, result.ProcessStartTime, result.PollStartedAt)
		return id, true, "poll failure followed by lower cumulative counter", err
	}

	if result.ProcessStartTime != nil && m.previous.Result.ProcessStartTime == nil {
		id, err := m.store.EnsureAppRun(ctx, result.TargetName, result.ProcessStartTime, result.PollStartedAt)
		return id, false, "", err
	}

	return *m.previous.AppRunID, false, "", m.touchAppRun(ctx, *m.previous.AppRunID, result.PollStartedAt)
}

func (m *Monitor) processStartChanged(result *collector.CollectionResult) bool {
	return m.previous != nil &&
		m.previous.Result.ProcessStartTime != nil &&
		result.ProcessStartTime != nil &&
		!m.previous.Result.ProcessStartTime.Equal(*result.ProcessStartTime)
}

func (m *Monitor) touchAppRun(ctx context.Context, appRunID int64, seenAt time.Time) error {
	return m.store.TouchAppRun(ctx, appRunID, seenAt)
}

func (m *Monitor) recordSuccess(at time.Time, pollID int64, snapshot *storage.Snapshot) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()

	m.latest = snapshot
	m.previous = snapshot
	m.status.LastPollAt = &at
	m.status.LastSuccessfulPollAt = &at
	m.status.ConsecutivePollFailures = 0
	m.status.LastPollErrorSummary = ""
	m.status.LastStoredPollID = pollID
	m.status.LastSuccessfulStoredPollID = pollID
}

func (m *Monitor) recordFailure(at time.Time, pollID int64, summary string, snapshot *storage.Snapshot) {
	m.statusMu.Lock()
	defer m.statusMu.Unlock()

	if snapshot != nil {
		m.latest = snapshot
	}
	m.status.LastPollAt = &at
	m.status.LastFailedPollAt = &at
	m.status.ConsecutivePollFailures++
	m.status.LastPollErrorSummary = summary
	m.status.LastStoredPollID = pollID
}

func hasErrorEvent(events []collector.CollectorEvent) bool {
	for _, event := range events {
		if event.Severity == collector.EventSeverityError {
			return true
		}
	}
	return false
}

func uptimeDecreased(previous, current []collector.MetricSample) bool {
	previousValue, previousOK := sampleValue(previous, "process_uptime")
	currentValue, currentOK := sampleValue(current, "process_uptime")
	return previousOK && currentOK && currentValue < previousValue
}

func decreasedCoreCounters(previous, current []collector.MetricSample) []string {
	keys := []string{"http_requests_total", "http_request_time_total_seconds"}
	var decreased []string
	for _, key := range keys {
		previousValue, previousOK := sampleValue(previous, key)
		currentValue, currentOK := sampleValue(current, key)
		if previousOK && currentOK && currentValue < previousValue {
			decreased = append(decreased, key)
		}
	}
	return decreased
}

func sampleValue(samples []collector.MetricSample, key string) (float64, bool) {
	for _, sample := range samples {
		if sample.Key == key {
			return sample.Value, true
		}
	}
	return 0, false
}
