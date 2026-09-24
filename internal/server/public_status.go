package server

import (
	"net/http"
	"regexp"
	"time"

	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/storage"
)

var publicURL = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)

// PublicStatusResponse is the current operational view for one configured target.
// Null timestamps and health values mean no observation is available.
type PublicStatusResponse struct {
	Target               string     `json:"target"`
	Type                 string     `json:"type"`
	CollectionStatus     string     `json:"collection_status"`
	LastPollAt           *time.Time `json:"last_poll_at"`
	LastSuccessfulPollAt *time.Time `json:"last_successful_poll_at"`
	LastFailedPollAt     *time.Time `json:"last_failed_poll_at"`
	ConsecutiveFailures  int        `json:"consecutive_collection_failures"`
	LastCollectionError  *string    `json:"last_collection_error"`
	ApplicationHealth    *string    `json:"application_health"`
	DependencyHealth     *string    `json:"dependency_health"`
	HealthObservedAt     *time.Time `json:"health_observed_at"`
}

func (s *Server) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireGETWithManager(w, r) {
		return
	}
	query, err := parsePublicQuery(r, "target")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target, err := selectPublicTarget(s.manager, query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if target.Metadata.Type == "" {
		http.Error(w, "target integration type is unavailable", http.StatusInternalServerError)
		return
	}
	status, successful, health := target.Monitor.CurrentState()
	writeJSON(w, http.StatusOK, publicStatus(target.Metadata.Name, target.Metadata.Type, status, successful, health))
}

func publicStatus(name, targetType string, status monitor.Status, successful *storage.Snapshot, health monitor.HealthObservation) PublicStatusResponse {
	response := PublicStatusResponse{
		Target:               name,
		Type:                 targetType,
		CollectionStatus:     "not_polled",
		LastPollAt:           status.LastPollAt,
		LastSuccessfulPollAt: status.LastSuccessfulPollAt,
		LastFailedPollAt:     status.LastFailedPollAt,
		ConsecutiveFailures:  status.ConsecutivePollFailures,
	}
	// On an ordinary restart, PollNow may load a prior successful snapshot
	// before its first new poll without restoring the monitor's status history.
	if response.LastSuccessfulPollAt == nil && successful != nil {
		at := successful.Result.PollFinishedAt.UTC()
		response.LastSuccessfulPollAt = &at
	}
	if status.LastPollAt != nil {
		response.CollectionStatus = "ok"
		if status.ConsecutivePollFailures > 0 {
			response.CollectionStatus = "error"
		}
	}
	if status.LastPollErrorSummary != "" {
		errorText := publicDiagnosticMessage(status.LastPollErrorSummary)
		response.LastCollectionError = &errorText
	}

	// A failed poll can report health even if storage fails. Otherwise retain
	// both values from the last successful snapshot.
	if !health.Reported && successful != nil {
		health = monitor.HealthObservation{
			Application: successful.Result.HealthStatus,
			Dependency:  successful.Result.DBHealthStatus,
			ObservedAt:  successful.Result.PollFinishedAt.UTC(),
			Reported:    successful.Result.HealthStatus != "" || successful.Result.DBHealthStatus != "",
		}
	}
	if health.Reported {
		if health.Application != "" {
			value := health.Application
			response.ApplicationHealth = &value
		}
		if health.Dependency != "" {
			value := health.Dependency
			response.DependencyHealth = &value
		}
		at := health.ObservedAt.UTC()
		response.HealthObservedAt = &at
	}
	return response
}

func publicDiagnosticMessage(message string) string {
	// Transport errors often repeat the endpoint address after the URL (for
	// example, in a dial error). Project the whole message to a safe category.
	if publicURL.MatchString(message) {
		return "collection request failed"
	}
	return message
}
