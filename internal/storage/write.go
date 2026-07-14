package storage

// This file persists normalized collection results, targets, app runs, and polls.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

func (s *Store) SaveCollectionResult(ctx context.Context, result *collector.CollectionResult) (int64, error) {
	appRunID, err := s.appRunIDForResult(ctx, result)
	if err != nil {
		return 0, err
	}
	return s.SaveCollectionResultWithAppRun(ctx, result, appRunID)
}

func (s *Store) SaveCollectionResultWithAppRun(ctx context.Context, result *collector.CollectionResult, appRunID *int64) (int64, error) {
	if result == nil {
		return 0, fmt.Errorf("collection result is required")
	}
	if result.TargetName == "" {
		return 0, fmt.Errorf("collection result target name is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	targetID, err := upsertTarget(ctx, tx, result.TargetName, result.PollStartedAt)
	if err != nil {
		return 0, err
	}

	status, errorSummary := pollStatus(result.Events)
	pollID, err := insertPoll(ctx, tx, targetID, appRunID, result, status, errorSummary)
	if err != nil {
		return 0, err
	}

	for _, sample := range result.Samples {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO metric_samples (poll_id, metric_key, metric_kind, value, unit)
VALUES (?, ?, ?, ?, ?)
`, pollID, sample.Key, string(sample.Kind), sample.Value, nullableString(sample.Unit)); err != nil {
			return 0, fmt.Errorf("insert metric sample %q: %w", sample.Key, err)
		}
	}

	for _, event := range result.Events {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO collector_events (poll_id, severity, event_type, metric_key, message)
VALUES (?, ?, ?, ?, ?)
`, pollID, string(event.Severity), event.Type, nullableString(event.MetricKey), event.Message); err != nil {
			return 0, fmt.Errorf("insert collector event %q: %w", event.Type, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit collection result: %w", err)
	}
	return pollID, nil
}

func (s *Store) EnsureAppRun(ctx context.Context, targetName string, processStartTime *time.Time, seenAt time.Time) (int64, error) {
	if strings.TrimSpace(targetName) == "" {
		return 0, fmt.Errorf("target name is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	targetID, err := upsertTarget(ctx, tx, targetName, seenAt)
	if err != nil {
		return 0, err
	}

	var appRunID int64
	if processStartTime != nil {
		id, err := upsertAppRun(ctx, tx, targetID, processStartTime, seenAt)
		if err != nil {
			return 0, err
		}
		appRunID = *id
	} else {
		id, err := insertAnonymousAppRun(ctx, tx, targetID, seenAt)
		if err != nil {
			return 0, err
		}
		appRunID = id
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit app run: %w", err)
	}
	return appRunID, nil
}

func (s *Store) TouchAppRun(ctx context.Context, appRunID int64, seenAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE app_runs
SET last_seen_at = ?
WHERE id = ?
`, formatTime(seenAt), appRunID)
	if err != nil {
		return fmt.Errorf("touch app run: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read touched app run count: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("app run %d not found", appRunID)
	}
	return nil
}

func (s *Store) appRunIDForResult(ctx context.Context, result *collector.CollectionResult) (*int64, error) {
	if result == nil || result.ProcessStartTime == nil {
		return nil, nil
	}
	id, err := s.EnsureAppRun(ctx, result.TargetName, result.ProcessStartTime, result.PollStartedAt)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func upsertTarget(ctx context.Context, tx *sql.Tx, name string, now time.Time) (int64, error) {
	if _, err := tx.ExecContext(ctx, `
INSERT INTO targets (name, created_at)
VALUES (?, ?)
ON CONFLICT(name) DO NOTHING
`, name, formatTime(now)); err != nil {
		return 0, fmt.Errorf("upsert target %q: %w", name, err)
	}

	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM targets WHERE name = ?`, name).Scan(&id); err != nil {
		return 0, fmt.Errorf("query target %q: %w", name, err)
	}
	return id, nil
}

func upsertAppRun(ctx context.Context, tx *sql.Tx, targetID int64, processStartTime *time.Time, seenAt time.Time) (*int64, error) {
	if processStartTime == nil {
		return nil, nil
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO app_runs (target_id, process_start_time, first_seen_at, last_seen_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(target_id, process_start_time) DO UPDATE SET
  last_seen_at = excluded.last_seen_at
`, targetID, formatTime(*processStartTime), formatTime(seenAt), formatTime(seenAt)); err != nil {
		return nil, fmt.Errorf("upsert app run: %w", err)
	}

	var id int64
	if err := tx.QueryRowContext(ctx, `
SELECT id FROM app_runs
WHERE target_id = ? AND process_start_time = ?
`, targetID, formatTime(*processStartTime)).Scan(&id); err != nil {
		return nil, fmt.Errorf("query app run: %w", err)
	}
	return &id, nil
}

func insertAnonymousAppRun(ctx context.Context, tx *sql.Tx, targetID int64, seenAt time.Time) (int64, error) {
	result, err := tx.ExecContext(ctx, `
INSERT INTO app_runs (target_id, process_start_time, first_seen_at, last_seen_at)
VALUES (?, NULL, ?, ?)
`, targetID, formatTime(seenAt), formatTime(seenAt))
	if err != nil {
		return 0, fmt.Errorf("insert app run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read inserted app run id: %w", err)
	}
	return id, nil
}

func insertPoll(ctx context.Context, tx *sql.Tx, targetID int64, appRunID *int64, result *collector.CollectionResult, status, errorSummary string) (int64, error) {
	insertResult, err := tx.ExecContext(ctx, `
INSERT INTO polls (
  target_id,
  app_run_id,
  started_at,
  finished_at,
  status,
  health_status,
  db_health_status,
  error_summary
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
`, targetID, nullableInt64(appRunID), formatTime(result.PollStartedAt), formatTime(result.PollFinishedAt), status, nullableString(result.HealthStatus), nullableString(result.DBHealthStatus), nullableString(errorSummary))
	if err != nil {
		return 0, fmt.Errorf("insert poll: %w", err)
	}

	pollID, err := insertResult.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read inserted poll id: %w", err)
	}
	return pollID, nil
}

func pollStatus(events []collector.CollectorEvent) (string, string) {
	var errors []string
	for _, event := range events {
		if event.Severity == collector.EventSeverityError {
			errors = append(errors, event.Message)
		}
	}
	if len(errors) == 0 {
		return "ok", ""
	}
	return "error", strings.Join(errors, "; ")
}
