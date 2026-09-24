package server

// This file parses request query parameters and applies request-scoped helpers.

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
)

func (s *Server) selectedTarget(r *http.Request) monitor.ManagedTarget {
	return s.manager.ResolveTarget(r.URL.Query().Get("target"))
}

func parsePublicQuery(r *http.Request, allowed ...string) (url.Values, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid query: %w", err)
	}
	allow := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allow[name] = true
	}
	for name, values := range query {
		if !allow[name] {
			return nil, fmt.Errorf("unsupported query parameter %q", name)
		}
		if len(values) != 1 {
			return nil, fmt.Errorf("query parameter %q must occur once", name)
		}
		if strings.TrimSpace(values[0]) == "" {
			return nil, fmt.Errorf("query parameter %q must not be empty", name)
		}
	}
	return query, nil
}

func selectPublicTarget(manager *monitor.Manager, query url.Values) (monitor.ManagedTarget, error) {
	name := query.Get("target")
	if name == "" {
		names := manager.Names()
		if len(names) != 1 {
			return monitor.ManagedTarget{}, fmt.Errorf("target is required when multiple targets are configured")
		}
		name = names[0]
	}
	target, ok := manager.ExactTarget(name)
	if !ok {
		return monitor.ManagedTarget{}, fmt.Errorf("unknown target %q", name)
	}
	return target, nil
}

func parsePublicEventsOptions(query url.Values) (time.Duration, int, error) {
	rangeValue := query.Get("range")
	var window time.Duration
	switch rangeValue {
	case "", "1h":
		window = time.Hour
	case "5m":
		window = 5 * time.Minute
	case "24h":
		window = 24 * time.Hour
	default:
		return 0, 0, fmt.Errorf("unsupported range %q; use 5m, 1h, or 24h", rangeValue)
	}
	limit := 100
	if value := query.Get("limit"); value != "" {
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return 0, 0, fmt.Errorf("limit must be a positive integer at most 500")
			}
		}
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 500 {
			return 0, 0, fmt.Errorf("limit must be a positive integer at most 500")
		}
		limit = parsed
	}
	return window, limit, nil
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
	return parseRangeAt(r, time.Now().UTC())
}

func parseRangeAt(r *http.Request, now time.Time) (time.Time, time.Time, DashboardRange, error) {
	query := r.URL.Query()
	now = now.UTC()
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
