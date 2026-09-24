package server

import (
	"net/http"
	"time"

	"github.com/pvrlabs/statlite/internal/storage"
)

// PublicMetricsResponse is the fixed one-hour operational metrics view.
type PublicMetricsResponse struct {
	Target                  string                      `json:"target"`
	Range                   string                      `json:"range"`
	BucketSeconds           int                         `json:"bucket_seconds"`
	EvaluatedAt             time.Time                   `json:"evaluated_at"`
	LatestHTTPObservationAt *time.Time                  `json:"latest_http_observation_at"`
	Points                  []PublicMetricPointResponse `json:"points"`
}

// PublicMetricPointResponse contains one occupied UTC minute slot.
type PublicMetricPointResponse struct {
	Timestamp          time.Time `json:"timestamp"`
	Requests           *float64  `json:"requests"`
	HTTP4xx            *float64  `json:"http_4xx"`
	HTTP5xx            *float64  `json:"http_5xx"`
	HTTP5xxRate        *float64  `json:"http_5xx_rate"`
	AverageLatencyMS   *float64  `json:"avg_latency_ms"`
	LatencyRequests    *float64  `json:"latency_requests"`
	ProcessCPUCores    *float64  `json:"process_cpu_cores"`
	RuntimeMemoryBytes *float64  `json:"runtime_memory_bytes"`
	HostCPUUsage       *float64  `json:"host_cpu_usage"`
	HostMemoryUsage    *float64  `json:"host_memory_usage"`
	HostDiskUsage      *float64  `json:"host_disk_usage"`
}

func (s *Server) handlePublicMetrics(w http.ResponseWriter, r *http.Request) {
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
	evaluatedAt := s.now().UTC()
	series, err := s.readPublicMetrics(r.Context(), target, evaluatedAt)
	if err != nil {
		http.Error(w, "query target metrics failed", http.StatusInternalServerError)
		return
	}
	response := PublicMetricsResponse{
		Target:                  target.Metadata.Name,
		Range:                   "1h",
		BucketSeconds:           60,
		EvaluatedAt:             evaluatedAt,
		LatestHTTPObservationAt: series.LatestHTTPObservationAt,
		Points:                  make([]PublicMetricPointResponse, 0, len(series.Points)),
	}
	for _, point := range series.Points {
		response.Points = append(response.Points, publicMetricPoint(point))
	}
	writeJSON(w, http.StatusOK, response)
}

func publicMetricPoint(point storage.PublicMetricPoint) PublicMetricPointResponse {
	var latencyMS *float64
	if point.AverageLatencySeconds != nil {
		value := *point.AverageLatencySeconds * 1000
		latencyMS = &value
	}
	return PublicMetricPointResponse{
		Timestamp:          point.Timestamp.UTC(),
		Requests:           point.Requests,
		HTTP4xx:            point.HTTP4xx,
		HTTP5xx:            point.HTTP5xx,
		HTTP5xxRate:        point.HTTP5xxRate,
		AverageLatencyMS:   latencyMS,
		LatencyRequests:    point.LatencyRequests,
		ProcessCPUCores:    point.ProcessCPUCores,
		RuntimeMemoryBytes: point.RuntimeMemoryBytes,
		HostCPUUsage:       point.HostCPUUsage,
		HostMemoryUsage:    point.HostMemoryUsage,
		HostDiskUsage:      point.HostDiskUsage,
	}
}
