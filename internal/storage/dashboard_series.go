package storage

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Match the size of the public metrics bound, comfortably within the supported
// SQLite build's parameter limit. Allow ordinary failed-poll groups as well as
// successful endpoints while keeping selection state and query size bounded.
const dashboardPollLimit = PublicMetricsPollLimit

// DashboardSeries returns endpoint-sampled chart points for the 7d/30d bucket
// scales, with independently derived native latest values. Other scales keep
// the ordinary series path. Callers aggregate the returned chart points.
//
// Long-range gauges and counters are approximate: skipped missing samples or
// same-run resets can change counter totals, bucket attribution, and latency.
// Raw samples remain authoritative; no stored data is changed.
func (s *Store) DashboardSeries(ctx context.Context, targetName string, start, end time.Time, bucket time.Duration) (*Series, error) {
	if bucket != 30*time.Minute && bucket != 2*time.Hour {
		return s.Series(ctx, targetName, start, end)
	}
	if strings.TrimSpace(targetName) == "" {
		return nil, fmt.Errorf("target name is required")
	}
	if !start.Before(end) {
		return nil, fmt.Errorf("series start must be before end")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin dashboard series read: %w", err)
	}
	defer tx.Rollback()
	series, err := s.dashboardSeries(ctx, tx, targetName, start, end, bucket)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit dashboard series read: %w", err)
	}
	return series, nil
}

func (s *Store) dashboardSeries(ctx context.Context, db seriesQuerier, targetName string, start, end time.Time, bucket time.Duration) (*Series, error) {
	ids, err := dashboardPollIDs(ctx, db, targetName, start, end, bucket)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		// Nil means selection exceeded the cap; empty means no polls. The
		// ordinary path handles both, including native latest on fallback.
		return s.seriesForPolls(ctx, db, targetName, start, end, ids, time.Time{})
	}
	previous, err := s.previousCounterValuesFrom(ctx, db, targetName, start, seriesCounterKeys, time.Time{})
	if err != nil {
		return nil, err
	}
	series, err := s.seriesWithBaseline(ctx, db, targetName, start, end, ids, maps.Clone(previous))
	if err != nil {
		return nil, err
	}

	// Find the last poll timestamp with a relevant sample, not just the last
	// attempted poll or the last selected endpoint. Reading every poll tied at
	// this timestamp preserves (started_at, id) counter ordering, including
	// per-counter baselines across missing samples and observed restarts.
	args := []any{targetName, formatSortableTime(start), formatSortableTime(end)}
	for _, key := range seriesMetricKeys {
		args = append(args, key)
	}
	var stamp string
	err = db.QueryRowContext(ctx, `
SELECT p.started_at FROM polls p JOIN targets t ON t.id = p.target_id
WHERE t.name = ? AND p.started_at >= ? AND p.started_at <= ?
  AND EXISTS (SELECT 1 FROM metric_samples ms WHERE ms.poll_id = p.id
    AND ms.metric_key IN (`+sqlPlaceholders(len(seriesMetricKeys))+`))
ORDER BY p.started_at DESC, p.id DESC LIMIT 1
`, args...).Scan(&stamp)
	if err == sql.ErrNoRows {
		return series, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query native latest series timestamp: %w", err)
	}
	latestStart, err := parseStoredTime(stamp)
	if err != nil {
		return nil, fmt.Errorf("parse native latest series timestamp: %w", err)
	}
	// Search only the requested window for newer native baselines. Missing
	// keys inherit the already-read pre-window baseline, avoiding a second
	// full-history scan per absent counter. Never use sampled counter state.
	if latestStart.After(start) {
		withinWindow, err := s.previousCounterValuesFrom(ctx, db, targetName, latestStart, seriesCounterKeys, start)
		if err != nil {
			return nil, err
		}
		maps.Copy(previous, withinWindow)
	}
	native, err := s.seriesWithBaseline(ctx, db, targetName, latestStart, end, nil, previous)
	if err != nil {
		return nil, err
	}
	series.LatestPoint = native.LatestPoint
	series.CurrentHostDisk = native.CurrentHostDisk
	// Retention clears the first native point's baseline. That point may be
	// absent from the selected endpoints, so preserve its identity separately.
	err = db.QueryRowContext(ctx, `
SELECT p.id FROM polls p JOIN targets t ON t.id = p.target_id
WHERE t.name = ? AND p.started_at >= ? AND p.started_at <= ?
  AND EXISTS (SELECT 1 FROM metric_samples ms WHERE ms.poll_id = p.id
    AND ms.metric_key IN (`+sqlPlaceholders(len(seriesMetricKeys))+`))
ORDER BY p.started_at ASC, p.id ASC LIMIT 1
`, args...).Scan(&series.FirstPollID)
	if err != nil {
		return nil, fmt.Errorf("query first native series poll: %w", err)
	}
	return series, nil
}

// dashboardPollIDs retains first/last polls per stable UTC bucket and app run.
// A nil result requests a raw fallback; an empty non-nil result is an empty
// window. Stop before either the endpoint list or selection state exceeds the
// cap, rather than constructing an unbounded IN query during restart churn.
func dashboardPollIDs(ctx context.Context, db seriesQuerier, targetName string, start, end time.Time, bucket time.Duration) ([]int64, error) {
	rows, err := db.QueryContext(ctx, `
SELECT p.id, p.started_at, p.app_run_id
FROM polls p JOIN targets t ON t.id = p.target_id
WHERE t.name = ? AND p.started_at >= ? AND p.started_at <= ?
ORDER BY p.started_at ASC, p.id ASC
`, targetName, formatSortableTime(start), formatSortableTime(end))
	if err != nil {
		return nil, fmt.Errorf("query dashboard polls: %w", err)
	}
	defer rows.Close()
	type group struct {
		bucket time.Time
		run    sql.NullInt64
	}
	type endpoints struct{ first, last int64 }
	groups := make(map[group]endpoints)
	count := 0
	for rows.Next() {
		var id int64
		var stamp string
		var run sql.NullInt64
		if err := rows.Scan(&id, &stamp, &run); err != nil {
			return nil, fmt.Errorf("scan dashboard poll: %w", err)
		}
		ts, err := parseStoredTime(stamp)
		if err != nil {
			return nil, fmt.Errorf("parse dashboard poll timestamp: %w", err)
		}
		key := group{clockBucketStart(ts, bucket), run}
		pair, exists := groups[key]
		if !exists || pair.first == pair.last {
			if count == dashboardPollLimit {
				return nil, nil
			}
			count++
		}
		if !exists {
			pair.first = id
		}
		pair.last = id
		groups[key] = pair
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dashboard polls: %w", err)
	}
	ids := make([]int64, 0, count)
	for _, pair := range groups {
		ids = append(ids, pair.first)
		if pair.last != pair.first {
			ids = append(ids, pair.last)
		}
	}
	slices.Sort(ids)
	return ids, nil
}
