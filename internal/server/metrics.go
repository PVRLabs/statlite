package server

import (
	"encoding/json"
	"log"
	"net/http"
	"runtime"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
)

type statliteMetricsResponse struct {
	Schema         string                `json:"schema"`
	Integration    string                `json:"integration"`
	Status         string                `json:"status"`
	DatabaseStatus *string               `json:"database_status,omitempty"`
	StartedAt      time.Time             `json:"started_at"`
	Metrics        statliteMetricsValues `json:"metrics"`
}

type statliteMetricsValues struct {
	RequestsTotal               uint64   `json:"requests_total"`
	Responses404Total           uint64   `json:"responses_404_total"`
	Responses4xxTotal           uint64   `json:"responses_4xx_total"`
	Responses5xxTotal           uint64   `json:"responses_5xx_total"`
	RequestDurationSecondsTotal float64  `json:"request_duration_seconds_total"`
	RequestDurationSecondsMax   float64  `json:"request_duration_seconds_max"`
	ProcessCPUUsage             float64  `json:"process_cpu_usage"`
	RuntimeHeapUsedBytes        uint64   `json:"runtime_heap_used_bytes"`
	UptimeSeconds               float64  `json:"uptime_seconds"`
	HostCPUUsage                *float64 `json:"host_cpu_usage,omitempty"`
	HostMemoryUsedBytes         *float64 `json:"host_memory_used_bytes,omitempty"`
	HostMemoryTotalBytes        *float64 `json:"host_memory_total_bytes,omitempty"`
	HostDiskUsedBytes           *float64 `json:"host_disk_used_bytes,omitempty"`
	HostDiskTotalBytes          *float64 `json:"host_disk_total_bytes,omitempty"`
}

func (s *Server) handleStatliteMetrics(w http.ResponseWriter, _ *http.Request) {
	sampledAt := time.Now()
	now := sampledAt.UTC()
	resources, warnings := s.resources(sampledAt)
	for _, warning := range warnings {
		log.Printf("StatLite metrics warning: %v", warning)
	}

	response := statliteMetricsResponse{
		Schema:         collector.StatliteMetricsV1Schema,
		Integration:    "statlite-self-monitoring",
		Status:         "UP",
		DatabaseStatus: s.databaseStatus(),
		StartedAt:      s.startedAt,
		Metrics: statliteMetricsValues{
			RequestsTotal:               s.requestsTotal.Load(),
			Responses404Total:           s.notFoundTotal.Load(),
			Responses4xxTotal:           s.clientErrors.Load(),
			Responses5xxTotal:           s.serverErrors.Load(),
			RequestDurationSecondsTotal: time.Duration(s.durationTotalNS.Load()).Seconds(),
			RequestDurationSecondsMax:   time.Duration(s.durationMaxNS.Load()).Seconds(),
			ProcessCPUUsage:             resources.processCPU,
			RuntimeHeapUsedBytes:        resources.processHeap,
			UptimeSeconds:               now.Sub(s.startedAt).Seconds(),
			HostCPUUsage:                resources.host.CPUUsage,
			HostMemoryUsedBytes:         resources.host.MemoryUsedBytes,
			HostMemoryTotalBytes:        resources.host.MemoryTotalBytes,
			HostDiskUsedBytes:           resources.host.DiskUsedBytes,
			HostDiskTotalBytes:          resources.host.DiskTotalBytes,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("encode StatLite metrics response: %v", err)
	}
}

func (s *Server) resources(now time.Time) (resourceSnapshot, []error) {
	s.resourceMu.Lock()
	defer s.resourceMu.Unlock()

	if !s.resourceSampledAt.IsZero() {
		age := now.Sub(s.resourceSampledAt)
		if age >= 0 && age < s.resourceInterval {
			return s.resourceSnapshot, nil
		}
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	host, warnings := s.hostSampler.Sample(s.filesystemPath)
	s.resourceSnapshot = resourceSnapshot{
		processHeap: mem.Alloc,
		processCPU:  s.processCPUUsage(now),
		host:        host,
	}
	s.resourceSampledAt = now
	return s.resourceSnapshot, warnings
}

func (s *Server) databaseStatus() *string {
	if s.manager == nil {
		return nil
	}

	status := "UP"
	if s.storageHealthStatus() != "ok" {
		status = "DOWN"
	}
	return &status
}
