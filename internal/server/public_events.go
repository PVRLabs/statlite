package server

import (
	"net/http"
	"time"

	"github.com/pvrlabs/statlite/internal/storage"
)

// PublicEventsResponse contains recent diagnostic events for one target.
type PublicEventsResponse struct {
	Target string        `json:"target"`
	Events []PublicEvent `json:"events"`
}

// PublicEvent's timestamp is the start time of the poll that recorded it.
type PublicEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Severity  string    `json:"severity"`
	Type      string    `json:"type"`
	Message   string    `json:"message"`
	MetricKey string    `json:"metric_key,omitempty"`
}

func (s *Server) handlePublicEvents(w http.ResponseWriter, r *http.Request) {
	if !s.requireGETWithManager(w, r) {
		return
	}
	query, err := parsePublicQuery(r, "target", "range", "limit")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target, err := selectPublicTarget(s.manager, query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	window, limit, err := parsePublicEventsOptions(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	end := time.Now().UTC()
	start, _, _ := s.clampToRetention(end.Add(-window))
	response := PublicEventsResponse{Target: target.Metadata.Name, Events: []PublicEvent{}}
	if !start.Before(end) {
		writeJSON(w, http.StatusOK, response)
		return
	}
	events, err := target.Monitor.Events(r.Context(), start, end, limit)
	if err != nil {
		http.Error(w, "query target events failed", http.StatusInternalServerError)
		return
	}
	for _, event := range events {
		response.Events = append(response.Events, publicEvent(event))
	}
	writeJSON(w, http.StatusOK, response)
}

func publicEvent(event storage.Event) PublicEvent {
	return PublicEvent{
		Timestamp: event.Timestamp.UTC(),
		Severity:  event.Severity,
		Type:      event.Type,
		Message:   publicDiagnosticMessage(event.Message),
		MetricKey: event.MetricKey,
	}
}
