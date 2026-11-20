package storage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

const attentionPollLimit = 4096

// Each scalar subquery rejects ambiguous duplicate counters. Polls with no
// usable counters still appear so partial collections break the baseline.
const attentionPollQuery = `
SELECT p.id, p.started_at, p.app_run_id,
  (SELECT CASE WHEN count(*) = 1 AND min(metric_kind) = 'counter' THEN min(value) END
    FROM metric_samples WHERE poll_id = p.id AND metric_key = 'http_requests_total'),
  (SELECT CASE WHEN count(*) = 1 AND min(metric_kind) = 'counter' THEN min(value) END
    FROM metric_samples WHERE poll_id = p.id AND metric_key = 'http_5xx_total')
FROM polls p
WHERE p.target_id = (SELECT id FROM targets WHERE name = ?)
  AND p.started_at >= ? AND p.started_at <= ?
ORDER BY p.started_at, p.id
LIMIT ?
`

type HTTPErrorWindow struct {
	Requests float64
	Errors   float64
	Adequate bool
	Covered  time.Duration
}

// AttentionHTTP5xx counts only intervals fully inside the evaluation window.
func (s *Store) AttentionHTTP5xx(ctx context.Context, target string, start, end time.Time, cadence time.Duration) (HTTPErrorWindow, error) {
	window := end.Sub(start)
	// A slower cadence may leave fewer than two polls entirely inside the window.
	if cadence <= 0 || cadence > window/2 {
		return HTTPErrorWindow{}, nil
	}
	rows, err := s.db.QueryContext(ctx, attentionPollQuery, target, formatSortableTime(start), formatSortableTime(end), attentionPollLimit+1)
	if err != nil {
		return HTTPErrorWindow{}, fmt.Errorf("query attention polls: %w", err)
	}
	defer rows.Close()

	const requestsKey, errorsKey = "http_requests_total", "http_5xx_total"
	maxGap := min(2*cadence, max(window/5, cadence+30*time.Second))
	// With slow polling, up to one cadence at each window edge can go
	// uncounted. Still require roughly one full interval and fresh evidence.
	minCoverage := min(window*4/5, max(window-2*cadence, cadence*9/10))
	var result HTTPErrorWindow
	var previousAt, lastCovered time.Time
	previous := make(map[string]counterValue, 2)
	poll := pollSamples{samples: make(map[string]sampleValue, 2)}
	for count := 1; rows.Next(); count++ {
		if count > attentionPollLimit {
			return HTTPErrorWindow{}, nil
		}
		var timestamp string
		var run sql.NullInt64
		var requests, errors sql.NullFloat64
		if err := rows.Scan(&poll.pollID, &timestamp, &run, &requests, &errors); err != nil {
			return HTTPErrorWindow{}, fmt.Errorf("scan attention poll: %w", err)
		}
		at, err := parseStoredTime(timestamp)
		if err != nil {
			return HTTPErrorWindow{}, fmt.Errorf("parse attention poll time: %w", err)
		}
		poll.appRunID = nil
		if run.Valid {
			poll.appRunID = &run.Int64
		}
		clear(poll.samples)
		setAttentionCounter(poll.samples, requestsKey, requests)
		setAttentionCounter(poll.samples, errorsKey, errors)
		requestDelta := counterDelta(&poll, previous, requestsKey)
		errorDelta := counterDelta(&poll, previous, errorsKey)
		gap := at.Sub(previousAt)
		if gap > 0 && gap <= maxGap && requestDelta != nil && errorDelta != nil &&
			matchingCounterBaseline(previous, requestsKey, errorsKey) && *errorDelta <= *requestDelta {
			result.Requests += *requestDelta
			result.Errors += *errorDelta
			result.Covered += gap
			lastCovered = at
		}
		clear(previous)
		updatePreviousCounters(&poll, previous)
		previousAt = at
	}
	if err := rows.Err(); err != nil {
		return HTTPErrorWindow{}, fmt.Errorf("read attention polls: %w", err)
	}
	result.Adequate = !lastCovered.IsZero() && lastCovered.Equal(previousAt) &&
		end.Sub(lastCovered) <= maxGap && result.Covered >= minCoverage &&
		!math.IsInf(result.Requests, 0) && !math.IsInf(result.Errors, 0)
	return result, nil
}

func setAttentionCounter(samples map[string]sampleValue, key string, value sql.NullFloat64) {
	if value.Valid && value.Float64 >= 0 && !math.IsInf(value.Float64, 0) {
		samples[key] = sampleValue{kind: collector.MetricKindCounter, value: value.Float64}
	}
}
