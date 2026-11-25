package server

import (
	"net/http"
	"time"
)

const attentionWindow = 10 * time.Minute

type attentionCondition struct {
	Check          string    `json:"check"`
	Severity       string    `json:"severity"`
	Start          time.Time `json:"start"`
	End            time.Time `json:"end"`
	Rate           float64   `json:"rate"`
	Threshold      float64   `json:"threshold"`
	Requests       float64   `json:"requests"`
	CoveredSeconds float64   `json:"covered_seconds"`
	Errors         float64   `json:"errors"`
}

type attentionResponse struct {
	Conditions    []attentionCondition `json:"conditions"`
	Target        string               `json:"target,omitempty"`
	ClearedChecks []string             `json:"cleared_checks,omitempty"`
}

func (s *Server) handleAttention(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET required"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	response := attentionResponse{Conditions: []attentionCondition{}}
	if s.manager == nil {
		writeJSON(w, http.StatusOK, response)
		return
	}

	target := s.selectedTarget(r)
	if name := r.URL.Query().Get("target"); name != "" {
		var ok bool
		target, ok = s.manager.ExactTarget(name)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown target"})
			return
		}
	}
	response.Target = target.Metadata.Name
	end := s.now().UTC()
	start := end.Add(-attentionWindow)
	window, err := target.Monitor.AttentionHTTP5xx(r.Context(), start, end)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "attention evaluation failed"})
		return
	}
	const check = "http_5xx_rate"
	threshold, minRequests, minErrors := s.attention.Thresholds()
	if window.Adequate && window.Requests >= float64(minRequests) {
		rate := window.Errors / window.Requests
		if window.Errors >= float64(minErrors) && rate >= threshold {
			response.Conditions = append(response.Conditions, attentionCondition{
				Check: check, Severity: "warning", Start: start, End: end,
				Rate: rate, Threshold: threshold,
				Requests: window.Requests, Errors: window.Errors, CoveredSeconds: window.Covered.Seconds(),
			})
		} else if rate < threshold {
			response.ClearedChecks = []string{check}
		}
	}
	writeJSON(w, http.StatusOK, response)
}
