package server

// This file parses request query parameters and applies request-scoped helpers.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
)

func (s *Server) selectedTarget(r *http.Request) monitor.ManagedTarget {
	return s.manager.ResolveTarget(r.URL.Query().Get("target"))
}

func (s *Server) clampToRetention(start time.Time) (time.Time, time.Time, bool) {
	if s.retentionDays <= 0 || s.retentionCutoff == nil {
		return start, time.Time{}, false
	}
	cutoff := s.retentionCutoff().UTC()
	if start.Before(cutoff) {
		return cutoff, cutoff, true
	}
	return start, cutoff, false
}

func clearCutoffCounterBaseline(series *storage.Series, cutoff time.Time) {
	if series == nil || len(series.Points) == 0 {
		return
	}
	// The first point at or after the retained cutoff is the new counter baseline.
	// Search by timestamp rather than assuming storage order.
	cutoffIndex := -1
	for i, point := range series.Points {
		if point.Timestamp.Before(cutoff) {
			continue
		}
		if cutoffIndex == -1 || point.Timestamp.Before(series.Points[cutoffIndex].Timestamp) {
			cutoffIndex = i
		}
	}
	if cutoffIndex == -1 {
		return
	}
	cutoffPoint := &series.Points[cutoffIndex]
	clearSeriesCounterFields(cutoffPoint)
	if series.LatestPoint != nil && series.LatestPoint.PollID == cutoffPoint.PollID {
		clearSeriesCounterFields(series.LatestPoint)
	}
}

func clearSeriesCounterFields(point *storage.SeriesPoint) {
	point.Requests = nil
	point.HTTP404 = nil
	point.HTTP4xx = nil
	point.HTTP5xx = nil
	point.AverageLatencySeconds = nil
}

func parseRange(r *http.Request) (time.Time, time.Time, DashboardRange, error) {
	query := r.URL.Query()
	now := time.Now().UTC()
	if query.Get("start") != "" || query.Get("end") != "" {
		start, err := parseQueryTime(query.Get("start"), "start")
		if err != nil {
			return time.Time{}, time.Time{}, "", err
		}
		end := now
		if query.Get("end") != "" {
			parsedEnd, err := parseQueryTime(query.Get("end"), "end")
			if err != nil {
				return time.Time{}, time.Time{}, "", err
			}
			end = parsedEnd
		}
		if !start.Before(end) {
			return time.Time{}, time.Time{}, "", fmt.Errorf("start must be before end")
		}
		return start, end, DashboardRangeCustom, nil
	}

	switch strings.ToLower(strings.TrimSpace(query.Get("range"))) {
	case "", "1h", "last_hour":
		return now.Add(-time.Hour), now, DashboardRange1H, nil
	case "24h":
		return now.Add(-24 * time.Hour), now, DashboardRange24H, nil
	case "7d":
		return now.AddDate(0, 0, -7), now, DashboardRange7D, nil
	case "30d":
		return now.AddDate(0, 0, -30), now, DashboardRange30D, nil
	default:
		return time.Time{}, time.Time{}, "", fmt.Errorf("unsupported range; use 1h, 24h, 7d, 30d, or start/end")
	}
}

func parseOptionalLimit(r *http.Request) (int, error) {
	value := strings.TrimSpace(r.URL.Query().Get("limit"))
	if value == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit <= 0 {
		return 0, fmt.Errorf("limit must be a positive integer")
	}
	return limit, nil
}

func parseQueryTime(value, name string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, fmt.Errorf("%s is required when using custom ranges", name)
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339", name)
	}
	return parsed.UTC(), nil
}
