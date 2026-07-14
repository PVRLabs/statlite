package server

// This file exposes StatLite self-health data and process runtime metrics.

import (
	"encoding/json"
	"net/http"
	"runtime"
	"syscall"
	"time"

	"github.com/pvrlabs/statlite/internal/monitor"
	"github.com/pvrlabs/statlite/internal/version"
)

type HealthResponse struct {
	Status    string                 `json:"status"`
	Version   string                 `json:"version"`
	Timestamp string                 `json:"timestamp"`
	Statlite  StatliteHealthResponse `json:"statlite"`
}

type StatliteHealthResponse struct {
	UptimeSeconds    float64               `json:"uptime_seconds"`
	ProcessStartTime time.Time             `json:"process_start_time"`
	HTTP             StatliteHTTPHealth    `json:"http"`
	Runtime          StatliteRuntimeHealth `json:"runtime"`
	Polling          StatlitePollingHealth `json:"polling"`
	Storage          StatliteStorageHealth `json:"storage"`
}

type StatliteHTTPHealth struct {
	RequestsTotal    uint64 `json:"requests_total"`
	NotFoundTotal    uint64 `json:"not_found_total"`
	ServerErrorTotal uint64 `json:"server_error_total"`
}

type StatliteRuntimeHealth struct {
	MemoryAllocBytes uint64  `json:"memory_alloc_bytes"`
	MemorySysBytes   uint64  `json:"memory_sys_bytes"`
	Goroutines       int     `json:"goroutines"`
	ProcessCPUUsage  float64 `json:"process_cpu_usage"`
}

type StatliteStorageHealth struct {
	Status           string `json:"status"`
	LastStoredPollID int64  `json:"last_stored_poll_id"`
}

type StatlitePollingHealth struct {
	ConsecutiveFailures  int        `json:"consecutive_failures"`
	LastPollAt           *time.Time `json:"last_poll_at"`
	LastSuccessfulPollAt *time.Time `json:"last_successful_poll_at"`
	LastFailedPollAt     *time.Time `json:"last_failed_poll_at"`
}

// handleHealthz reports StatLite process health.
//
// Semantics:
//   - Top-level status/HTTP code reflect process readiness, not monitored-target health.
//   - Target poll failures are exposed under statlite.polling and do not mark the process unhealthy.
//   - SQLite storage check failure sets status to "error" and returns HTTP 503.
//   - When no monitor/manager is configured, storage is reported as "unavailable" and the process stays healthy.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	now := time.Now().UTC()
	cpuUsage := s.processCPUUsage(now)

	status := monitor.Status{}
	storageStatus := "unavailable"
	if s.manager != nil {
		target := s.selectedTarget(r)
		status = target.Monitor.Status()
		if target.Monitor.StorageHealthy(r.Context()) {
			storageStatus = "ok"
		} else {
			storageStatus = "error"
		}
	}

	// Process health is independent of monitored-target poll success/failure.
	// A failing self-monitor first poll (server not ready yet) must not make
	// StatLite report itself as permanently unhealthy.
	processStatus := "ok"
	httpCode := http.StatusOK
	if storageStatus == "error" {
		processStatus = "error"
		httpCode = http.StatusServiceUnavailable
	}

	w.WriteHeader(httpCode)
	json.NewEncoder(w).Encode(HealthResponse{
		Status:    processStatus,
		Version:   version.Version,
		Timestamp: now.Format(time.RFC3339),
		Statlite: StatliteHealthResponse{
			UptimeSeconds:    now.Sub(s.startedAt).Seconds(),
			ProcessStartTime: s.startedAt,
			HTTP: StatliteHTTPHealth{
				RequestsTotal:    s.requestsTotal.Load(),
				NotFoundTotal:    s.notFoundTotal.Load(),
				ServerErrorTotal: s.serverErrors.Load(),
			},
			Runtime: StatliteRuntimeHealth{
				MemoryAllocBytes: mem.Alloc,
				MemorySysBytes:   mem.Sys,
				Goroutines:       runtime.NumGoroutine(),
				ProcessCPUUsage:  cpuUsage,
			},
			Storage: StatliteStorageHealth{
				Status:           storageStatus,
				LastStoredPollID: status.LastStoredPollID,
			},
			Polling: StatlitePollingHealth{
				ConsecutiveFailures:  status.ConsecutivePollFailures,
				LastPollAt:           status.LastPollAt,
				LastSuccessfulPollAt: status.LastSuccessfulPollAt,
				LastFailedPollAt:     status.LastFailedPollAt,
			},
		},
	})
}

func (s *Server) processCPUUsage(now time.Time) float64 {
	cpuSeconds, err := processCPUSeconds()
	if err != nil {
		return 0
	}

	s.cpuMu.Lock()
	defer s.cpuMu.Unlock()

	if s.lastCPUAt.IsZero() {
		s.lastCPUAt = now
		s.lastCPUSeconds = cpuSeconds
		return 0
	}

	elapsed := now.Sub(s.lastCPUAt).Seconds()
	cpuDelta := cpuSeconds - s.lastCPUSeconds
	s.lastCPUAt = now
	s.lastCPUSeconds = cpuSeconds
	if elapsed <= 0 || cpuDelta < 0 {
		return s.lastCPUUsage
	}

	usage := cpuDelta / elapsed / float64(runtime.NumCPU())
	if usage < 0 {
		usage = 0
	}
	s.lastCPUUsage = usage
	return usage
}

func processCPUSeconds() (float64, error) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, err
	}
	return timevalSeconds(usage.Utime) + timevalSeconds(usage.Stime), nil
}

func timevalSeconds(value syscall.Timeval) float64 {
	return float64(value.Sec) + float64(value.Usec)/1_000_000
}
