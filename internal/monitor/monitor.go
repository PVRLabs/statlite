package monitor

// This file runs polling cycles and exposes cached target status and query data.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/storage"
)

type Collector interface {
	Collect(context.Context) (*collector.CollectionResult, error)
}

type Monitor struct {
	targetName           string
	collector            Collector
	store                *storage.Store
	interval             time.Duration
	startupFollowUpDelay time.Duration
	noPoll               atomic.Bool

	pollMu sync.Mutex

	statusMu sync.RWMutex
	status   Status
	latest   *storage.Snapshot
	previous *storage.Snapshot
}

const defaultStartupFollowUpDelay = 3 * time.Second

var ErrPollingDisabled = errors.New("polling is disabled by --no-poll")

// EventTypeRestartDetected is recorded when detectAppRun observes a new app run.
// Storage treats event types as opaque strings; this constant lives with the producer.
const EventTypeRestartDetected = "restart_detected"

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
		targetName:           targetName,
		collector:            collector,
		store:                store,
		interval:             interval,
		startupFollowUpDelay: defaultStartupFollowUpDelay,
	}, nil
}

func (m *Monitor) Start(ctx context.Context) {
	go m.loop(ctx)
}

// EnableNoPoll prevents all collection and restores the monitor's current
// dashboard state from its most recent stored poll.
func (m *Monitor) EnableNoPoll(ctx context.Context) error {
	m.noPoll.Store(true)

	snapshot, err := m.store.LatestSnapshot(ctx, m.targetName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	successful, successfulErr := m.store.LatestSuccessfulSnapshot(ctx, m.targetName)
	if successfulErr != nil && !errors.Is(successfulErr, sql.ErrNoRows) {
		return successfulErr
	}
	failedPolls, err := m.store.TrailingFailedPolls(ctx, m.targetName)
	if err != nil {
		return err
	}
	lastFailedAt, failedErr := m.store.LatestFailedPollAt(ctx, m.targetName)
	if failedErr != nil && !errors.Is(failedErr, sql.ErrNoRows) {
		return failedErr
	}

	at := snapshot.Result.PollFinishedAt
	m.statusMu.Lock()
	defer m.statusMu.Unlock()
	m.latest = snapshot
	m.status.LastPollAt = &at
	m.status.LastStoredPollID = snapshot.PollID
	if successfulErr == nil {
		successfulAt := successful.Result.PollFinishedAt
		m.previous = successful
		m.status.LastSuccessfulPollAt = &successfulAt
		m.status.LastSuccessfulStoredPollID = successful.PollID
	}
	if failedErr == nil {
		m.status.LastFailedPollAt = lastFailedAt
	}
	if snapshot.Status != "ok" {
		m.status.ConsecutivePollFailures = failedPolls
		m.status.LastPollErrorSummary = snapshot.ErrorSummary
	}
	return nil
}

func (m *Monitor) TargetName() string {
	return m.targetName
}

func (m *Monitor) PollNow(ctx context.Context) (*storage.Snapshot, error) {
	if m.noPoll.Load() {
		return nil, ErrPollingDisabled
	}
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
		if collectErr == nil {
			collectErr = errors.New("collector returned no result")
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
				Type:     EventTypeRestartDetected,
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

func (m *Monitor) StorageAvailable() bool {
	return m.store.Available()
}

func (m *Monitor) Series(ctx context.Context, start, end time.Time) (*storage.Series, error) {
	return m.store.Series(ctx, m.targetName, start, end)
}

func (m *Monitor) Events(ctx context.Context, start, end time.Time, limit int) ([]storage.Event, error) {
	return m.store.Events(ctx, m.targetName, start, end, limit)
}

func (m *Monitor) LatestEventByType(ctx context.Context, eventType string, start, end time.Time) (*storage.Event, error) {
	return m.store.LatestEventByType(ctx, m.targetName, eventType, start, end)
}

func (m *Monitor) loop(ctx context.Context) {
	previous, historyLoaded := m.loadSuccessfulHistory(ctx)
	if snapshot, err := m.PollNow(ctx); historyLoaded && needsStartupFollowUp(previous, snapshot, err) {
		timer := time.NewTimer(startupFollowUpWait(m.interval, m.startupFollowUpDelay))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			m.PollNow(ctx)
		}
	}

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

func needsStartupFollowUp(previous, current *storage.Snapshot, pollErr error) bool {
	if pollErr != nil || current == nil || current.Status != "ok" || !hasCounterSamples(current.Result.Samples) {
		return false
	}
	if previous == nil || appRunChanged(previous.AppRunID, current.AppRunID) {
		return true
	}
	return hasCounterWithoutBaseline(previous.Result.Samples, current.Result.Samples)
}

func appRunChanged(previous, current *int64) bool {
	if previous == nil || current == nil {
		return previous != current
	}
	return *previous != *current
}

func startupFollowUpWait(interval, preferred time.Duration) time.Duration {
	if interval < preferred {
		return interval
	}
	return preferred
}

func hasCounterSamples(samples []collector.MetricSample) bool {
	for _, sample := range samples {
		if sample.Kind == collector.MetricKindCounter {
			return true
		}
	}
	return false
}

func hasCounterWithoutBaseline(previous, current []collector.MetricSample) bool {
	previousCounters := make(map[string]struct{})
	for _, sample := range previous {
		if sample.Kind == collector.MetricKindCounter {
			previousCounters[sample.Key] = struct{}{}
		}
	}
	for _, sample := range current {
		if sample.Kind != collector.MetricKindCounter {
			continue
		}
		if _, ok := previousCounters[sample.Key]; !ok {
			return true
		}
	}
	return false
}

func (m *Monitor) loadSuccessfulHistory(ctx context.Context) (*storage.Snapshot, bool) {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()

	if m.previous != nil {
		return m.previous, true
	}
	previous, err := m.store.LatestSuccessfulSnapshot(ctx, m.targetName)
	if err == nil {
		m.previous = previous
		return previous, true
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, true
	}
	// A storage error is handled by PollNow. Avoid adding a follow-up poll when
	// StatLite cannot determine whether this target already has history.
	return nil, false
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
