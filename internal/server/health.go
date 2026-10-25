package server

// This file exposes lightweight StatLite readiness.

import (
	"context"
	"net/http"
	"time"

	"github.com/pvrlabs/statlite/internal/version"
)

const (
	storageHealthCheckInterval = time.Minute
	storageHealthCheckTimeout  = time.Second
)

type HealthResponse struct {
	Status    string                `json:"status"`
	Version   string                `json:"version"`
	Timestamp string                `json:"timestamp"`
	Storage   StatliteStorageHealth `json:"storage"`
}

type StatliteStorageHealth struct {
	Status string `json:"status"`
}

// handleHealthz reports StatLite process health.
//
// Semantics:
//   - Top-level status/HTTP code reflect process readiness, not monitored-target health.
//   - Cached SQLite storage check failure sets status to "error" and returns HTTP 503.
//   - When no monitor/manager is configured, storage is reported as "unavailable" and the process stays healthy.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()

	storageStatus := s.storageHealthStatus()

	// Process health is independent of monitored-target poll success/failure.
	// A failing self-monitor first poll (server not ready yet) must not make
	// StatLite report itself as permanently unhealthy.
	processStatus := "ok"
	httpCode := http.StatusOK
	if storageStatus != "ok" && storageStatus != "unavailable" {
		processStatus = "error"
		httpCode = http.StatusServiceUnavailable
	}

	writeJSON(w, httpCode, HealthResponse{
		Status:    processStatus,
		Version:   version.Version,
		Timestamp: now.Format(time.RFC3339),
		Storage:   StatliteStorageHealth{Status: storageStatus},
	})
}

func (s *Server) startStorageHealthChecks() {
	if s.storageHealthy == nil {
		return
	}

	s.storageHealthMu.Lock()
	if s.storageCancel != nil {
		s.storageHealthMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.storageCancel = cancel
	s.storageHealthMu.Unlock()

	s.refreshStorageHealth(ctx)
	go func() {
		ticker := time.NewTicker(s.storageInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.refreshStorageHealth(ctx)
			}
		}
	}()
}

func (s *Server) stopStorageHealthChecks() {
	s.storageHealthMu.Lock()
	cancel := s.storageCancel
	s.storageCancel = nil
	s.storageHealthMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Server) refreshStorageHealth(parent context.Context) {
	if s.storageHealthy == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, storageHealthCheckTimeout)
	defer cancel()

	status := "ok"
	if !s.storageHealthy(ctx) {
		status = "error"
	}
	s.storageHealthMu.Lock()
	s.storageHealth = status
	s.storageHealthMu.Unlock()
}

func (s *Server) storageHealthStatus() string {
	if s.storageAvailable == nil {
		return "unavailable"
	}
	if !s.storageAvailable() {
		return "error"
	}
	s.storageHealthMu.RLock()
	defer s.storageHealthMu.RUnlock()
	return s.storageHealth
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

	usage := cpuDelta / elapsed
	if usage < 0 {
		usage = 0
	}
	s.lastCPUUsage = usage
	return usage
}
