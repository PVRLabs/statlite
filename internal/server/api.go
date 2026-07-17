package server

// This file serves JSON API and debug endpoints backed by monitor state.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
)

const (
	restartStatusFound        = "found"
	restartStatusNone         = "none"
	restartStatusInvalidRange = "invalid_range"
	restartStatusUnavailable  = "unavailable"
)

type SummaryResponse struct {
	Targets        []monitor.TargetSummary `json:"targets"`
	SelectedTarget monitor.TargetMetadata  `json:"selected_target"`
	Monitor        monitor.Status          `json:"monitor"`
	Latest         interface{}             `json:"latest,omitempty"`
	// LatestRestart is the newest restart_detected timestamp in the selected
	// dashboard range (same range query params as series/events). Kept separate
	// from /api/events so the Recent events LIMIT does not hide restarts.
	LatestRestart       *time.Time `json:"latest_restart,omitempty"`
	LatestRestartStatus string     `json:"latest_restart_status"`
	Now                 time.Time  `json:"now"`
}

func (s *Server) handleDebugPollNow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "monitor is not configured", http.StatusInternalServerError)
		return
	}

	target := s.selectedTarget(r)
	snapshot, err := target.Monitor.PollNow(r.Context())

	status := http.StatusOK
	if err != nil {
		status = http.StatusBadGateway
	}
	writeJSON(w, status, snapshot)
}

func (s *Server) handleDebugLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.manager == nil {
		http.Error(w, "monitor is not configured", http.StatusInternalServerError)
		return
	}
	target := s.selectedTarget(r)
	latest := target.Monitor.LatestSnapshot()
	if latest == nil {
		http.Error(w, fmt.Sprintf("no poll has run yet for target %q", target.Metadata.Name), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, latest)
}

func (s *Server) handleMonitorStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "monitor is not configured", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, s.selectedTarget(r).Monitor.Status())
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "monitor is not configured", http.StatusInternalServerError)
		return
	}
	target := s.selectedTarget(r)
	resp := SummaryResponse{
		Targets:             s.manager.Summaries(),
		SelectedTarget:      target.Metadata,
		Monitor:             target.Monitor.Status(),
		Latest:              target.Monitor.LatestSnapshot(),
		LatestRestartStatus: restartStatusNone,
		Now:                 time.Now().UTC(),
	}
	// Restart history is optional enrichment. Preserve the core in-memory
	// summary when range parsing or its storage lookup fails, and report why the
	// timestamp is absent so callers can distinguish failures from no restart.
	if start, end, _, err := parseRange(r); err != nil {
		resp.LatestRestartStatus = restartStatusInvalidRange
	} else {
		start, _, _ = s.clampToRetention(start)
		if start.Before(end) {
			restart, err := target.Monitor.LatestEventByType(r.Context(), monitor.EventTypeRestartDetected, start, end)
			if err != nil {
				resp.LatestRestartStatus = restartStatusUnavailable
			} else if restart != nil {
				ts := restart.Timestamp.UTC()
				resp.LatestRestart = &ts
				resp.LatestRestartStatus = restartStatusFound
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLatest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "monitor is not configured", http.StatusInternalServerError)
		return
	}
	target := s.selectedTarget(r)
	latest := target.Monitor.LatestSnapshot()
	if latest == nil {
		http.Error(w, fmt.Sprintf("no poll has run yet for target %q", target.Metadata.Name), http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, latest)
}

func (s *Server) handleSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "monitor is not configured", http.StatusInternalServerError)
		return
	}

	start, end, dashRange, err := parseRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	start, cutoff, clamped := s.clampToRetention(start)
	if !start.Before(end) {
		writeJSON(w, http.StatusOK, &storage.Series{Start: start, End: end, Points: []storage.SeriesPoint{}})
		return
	}
	target := s.selectedTarget(r)
	series, err := target.Monitor.Series(r.Context(), start, end)
	if err != nil {
		http.Error(w, fmt.Sprintf("target %q: %v", target.Metadata.Name, err), http.StatusInternalServerError)
		return
	}
	if clamped {
		clearCutoffCounterBaseline(series, cutoff)
	}
	// Aggregate after restart-aware deltas using the explicit dashboard scale.
	// Sparse series (at most one point per bucket) stay at native resolution.
	series = storage.AggregateSeries(series, dashboardBucketDuration(dashRange))
	writeJSON(w, http.StatusOK, series)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "monitor is not configured", http.StatusInternalServerError)
		return
	}

	start, end, _, err := parseRange(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	limit, err := parseOptionalLimit(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	start, _, _ = s.clampToRetention(start)
	if !start.Before(end) {
		writeJSON(w, http.StatusOK, []storage.Event{})
		return
	}
	target := s.selectedTarget(r)
	// The endpoint remains unlimited by default. Dashboard callers request their
	// own bounded Recent events result through the optional SQL-backed limit.
	events, err := target.Monitor.Events(r.Context(), start, end, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("target %q: %v", target.Metadata.Name, err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
