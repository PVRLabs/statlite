package storage

// This file loads latest snapshots and collector events from SQLite.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

func (s *Store) LatestSnapshot(ctx context.Context, targetName string) (*Snapshot, error) {
	if strings.TrimSpace(targetName) == "" {
		return nil, fmt.Errorf("target name is required")
	}

	return s.latestSnapshot(ctx, targetName, "")
}

func (s *Store) LatestSuccessfulSnapshot(ctx context.Context, targetName string) (*Snapshot, error) {
	if strings.TrimSpace(targetName) == "" {
		return nil, fmt.Errorf("target name is required")
	}

	return s.latestSnapshot(ctx, targetName, "ok")
}

func (s *Store) latestSnapshot(ctx context.Context, targetName, status string) (*Snapshot, error) {
	statusFilter := ""
	args := []interface{}{targetName}
	if status != "" {
		statusFilter = "AND p.status = ?"
		args = append(args, status)
	}

	row := s.db.QueryRowContext(ctx, `
SELECT
  p.id,
  t.id,
  p.app_run_id,
  p.started_at,
  p.finished_at,
  p.status,
  p.health_status,
  p.db_health_status,
  p.error_summary
FROM polls p
JOIN targets t ON t.id = p.target_id
WHERE t.name = ?
`+statusFilter+`
ORDER BY p.started_at DESC, p.id DESC
LIMIT 1
`, args...)

	var snapshot Snapshot
	var appRunID sql.NullInt64
	var startedAt, finishedAt string
	var healthStatus, dbHealthStatus, errorSummary sql.NullString
	if err := row.Scan(
		&snapshot.PollID,
		&snapshot.TargetID,
		&appRunID,
		&startedAt,
		&finishedAt,
		&snapshot.Status,
		&healthStatus,
		&dbHealthStatus,
		&errorSummary,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, sql.ErrNoRows
		}
		return nil, fmt.Errorf("query latest snapshot: %w", err)
	}

	if appRunID.Valid {
		snapshot.AppRunID = &appRunID.Int64
	}
	if errorSummary.Valid {
		snapshot.ErrorSummary = errorSummary.String
	}
	snapshot.Result.TargetName = targetName
	parsedStartedAt, err := parseTime(startedAt)
	if err != nil {
		return nil, fmt.Errorf("parse poll started_at: %w", err)
	}
	parsedFinishedAt, err := parseTime(finishedAt)
	if err != nil {
		return nil, fmt.Errorf("parse poll finished_at: %w", err)
	}
	snapshot.Result.PollStartedAt = parsedStartedAt
	snapshot.Result.PollFinishedAt = parsedFinishedAt
	if healthStatus.Valid {
		snapshot.Result.HealthStatus = healthStatus.String
	}
	if dbHealthStatus.Valid {
		snapshot.Result.DBHealthStatus = dbHealthStatus.String
	}

	if err := s.loadSamples(ctx, snapshot.PollID, &snapshot.Result); err != nil {
		return nil, err
	}
	if err := s.loadEvents(ctx, snapshot.PollID, &snapshot.Result); err != nil {
		return nil, err
	}
	if snapshot.AppRunID != nil {
		processStartTime, err := s.processStartTime(ctx, *snapshot.AppRunID)
		if err != nil {
			return nil, err
		}
		snapshot.Result.ProcessStartTime = processStartTime
	}

	return &snapshot, nil
}

func (s *Store) loadSamples(ctx context.Context, pollID int64, result *collector.CollectionResult) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT metric_key, metric_kind, value, unit
FROM metric_samples
WHERE poll_id = ?
ORDER BY id
`, pollID)
	if err != nil {
		return fmt.Errorf("query metric samples: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var sample collector.MetricSample
		var kind string
		var unit sql.NullString
		if err := rows.Scan(&sample.Key, &kind, &sample.Value, &unit); err != nil {
			return fmt.Errorf("scan metric sample: %w", err)
		}
		sample.Kind = collector.MetricKind(kind)
		if unit.Valid {
			sample.Unit = unit.String
		}
		result.Samples = append(result.Samples, sample)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate metric samples: %w", err)
	}
	return nil
}

func (s *Store) loadEvents(ctx context.Context, pollID int64, result *collector.CollectionResult) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT severity, event_type, metric_key, message
FROM collector_events
WHERE poll_id = ?
ORDER BY id
`, pollID)
	if err != nil {
		return fmt.Errorf("query collector events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var event collector.CollectorEvent
		var severity string
		var metricKey sql.NullString
		if err := rows.Scan(&severity, &event.Type, &metricKey, &event.Message); err != nil {
			return fmt.Errorf("scan collector event: %w", err)
		}
		event.Severity = collector.EventSeverity(severity)
		if metricKey.Valid {
			event.MetricKey = metricKey.String
		}
		result.Events = append(result.Events, event)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate collector events: %w", err)
	}
	return nil
}

func (s *Store) processStartTime(ctx context.Context, appRunID int64) (*time.Time, error) {
	var value sql.NullString
	if err := s.db.QueryRowContext(ctx, `
SELECT process_start_time FROM app_runs WHERE id = ?
`, appRunID).Scan(&value); err != nil {
		return nil, fmt.Errorf("query app run process start time: %w", err)
	}
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, fmt.Errorf("parse app run process start time: %w", err)
	}
	return &parsed, nil
}

func (s *Store) Events(ctx context.Context, targetName string, start, end time.Time, limit int) ([]Event, error) {
	if strings.TrimSpace(targetName) == "" {
		return nil, fmt.Errorf("target name is required")
	}
	if !start.Before(end) {
		return nil, fmt.Errorf("events start must be before end")
	}

	query, args := eventsQuery(targetName, start, end, "", limit)
	return s.scanEvents(ctx, query, args)
}

// LatestEventByType returns the newest event of the given type in [start, end], or nil when none exist.
func (s *Store) LatestEventByType(ctx context.Context, targetName, eventType string, start, end time.Time) (*Event, error) {
	if strings.TrimSpace(targetName) == "" {
		return nil, fmt.Errorf("target name is required")
	}
	if strings.TrimSpace(eventType) == "" {
		return nil, fmt.Errorf("event type is required")
	}
	if !start.Before(end) {
		return nil, fmt.Errorf("events start must be before end")
	}

	query, args := eventsQuery(targetName, start, end, eventType, 1)
	events, err := s.scanEvents(ctx, query, args)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}
	return &events[0], nil
}

func (s *Store) scanEvents(ctx context.Context, query string, args []interface{}) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		var startedAt string
		var metricKey sql.NullString
		if err := rows.Scan(&event.PollID, &startedAt, &event.Severity, &event.Type, &metricKey, &event.Message); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		parsedStartedAt, err := parseTime(startedAt)
		if err != nil {
			return nil, fmt.Errorf("parse event started_at: %w", err)
		}
		event.Timestamp = parsedStartedAt
		if metricKey.Valid {
			event.MetricKey = metricKey.String
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return events, nil
}

// eventsQuery builds the events SELECT. When limit > 0, a SQL LIMIT is applied
// after the descending order so only the newest rows are loaded into Go.
// When eventType is non-empty, results are restricted to that collector event type.
func eventsQuery(targetName string, start, end time.Time, eventType string, limit int) (string, []interface{}) {
	query := `
SELECT p.id, p.started_at, e.severity, e.event_type, e.metric_key, e.message
FROM collector_events e
JOIN polls p ON p.id = e.poll_id
JOIN targets t ON t.id = p.target_id
WHERE t.name = ?
  AND p.started_at >= ?
  AND p.started_at <= ?
`
	args := []interface{}{targetName, formatTime(start), formatTime(end)}
	if eventType != "" {
		query += "  AND e.event_type = ?\n"
		args = append(args, eventType)
	}
	query += "ORDER BY p.started_at DESC, p.id DESC, e.id DESC\n"
	if limit > 0 {
		query += "LIMIT ?\n"
		args = append(args, limit)
	}
	return query, args
}
