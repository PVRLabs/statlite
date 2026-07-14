package server

// This file parses request query parameters and applies request-scoped helpers.

import (
	"fmt"
	"net/http"
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
	series.Points[cutoffIndex].Requests = nil
	series.Points[cutoffIndex].HTTP404 = nil
	series.Points[cutoffIndex].HTTP4xx = nil
	series.Points[cutoffIndex].HTTP5xx = nil
	series.Points[cutoffIndex].AverageLatencySeconds = nil
}

func parseRange(r *http.Request) (time.Time, time.Time, error) {
	query := r.URL.Query()
	now := time.Now().UTC()
	if query.Get("start") != "" || query.Get("end") != "" {
		start, err := parseQueryTime(query.Get("start"), "start")
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		end := now
		if query.Get("end") != "" {
			parsedEnd, err := parseQueryTime(query.Get("end"), "end")
			if err != nil {
				return time.Time{}, time.Time{}, err
			}
			end = parsedEnd
		}
		if !start.Before(end) {
			return time.Time{}, time.Time{}, fmt.Errorf("start must be before end")
		}
		return start, end, nil
	}

	switch strings.ToLower(strings.TrimSpace(query.Get("range"))) {
	case "", "1h", "last_hour":
		return now.Add(-time.Hour), now, nil
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), now, nil
	case "7d":
		return now.AddDate(0, 0, -7), now, nil
	case "30d":
		return now.AddDate(0, 0, -30), now, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unsupported range; use 1h, today, 7d, 30d, or start/end")
	}
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
