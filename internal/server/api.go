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

type SummaryResponse struct {
	Targets        []monitor.TargetSummary `json:"targets"`
	SelectedTarget monitor.TargetMetadata  `json:"selected_target"`
	Monitor        monitor.Status          `json:"monitor"`
	Latest         interface{}             `json:"latest,omitempty"`
	Now            time.Time               `json:"now"`
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
	writeJSON(w, http.StatusOK, SummaryResponse{
		Targets:        s.manager.Summaries(),
		SelectedTarget: target.Metadata,
		Monitor:        target.Monitor.Status(),
		Latest:         target.Monitor.LatestSnapshot(),
		Now:            time.Now().UTC(),
	})
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

	start, end, err := parseRange(r)
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

	start, end, err := parseRange(r)
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
	events, err := target.Monitor.Events(r.Context(), start, end)
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
