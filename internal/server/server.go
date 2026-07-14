package server

// This file owns HTTP server construction, route registration, and lifecycle.

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pvrlabs/statlite/internal/monitor"
)

type Server struct {
	httpServer      *http.Server
	manager         *monitor.Manager
	retentionDays   int
	retentionCutoff func() time.Time
	startedAt       time.Time
	requestsTotal   atomic.Uint64
	notFoundTotal   atomic.Uint64
	serverErrors    atomic.Uint64
	cpuMu           sync.Mutex
	lastCPUAt       time.Time
	lastCPUSeconds  float64
	lastCPUUsage    float64
}

func New(listen string, mon *monitor.Monitor) *Server {
	var manager *monitor.Manager
	if mon != nil {
		var err error
		manager, err = monitorManagerForSingleTarget(mon)
		if err != nil {
			panic(err)
		}
	}
	return NewWithManager(listen, manager)
}

func NewWithManager(listen string, manager *monitor.Manager) *Server {
	return NewWithManagerRetention(listen, manager, 0)
}

func NewWithManagerRetention(listen string, manager *monitor.Manager, retentionDays int) *Server {
	return NewWithManagerRetentionCutoff(listen, manager, retentionDays, nil)
}

func NewWithManagerRetentionCutoff(listen string, manager *monitor.Manager, retentionDays int, retentionCutoff func() time.Time) *Server {
	mux := http.NewServeMux()
	if retentionDays > 0 && retentionCutoff == nil {
		retentionCutoff = func() time.Time {
			return time.Now().UTC().AddDate(0, 0, -retentionDays)
		}
	}
	s := &Server{
		manager:         manager,
		retentionDays:   retentionDays,
		retentionCutoff: retentionCutoff,
		startedAt:       time.Now().UTC(),
	}

	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/static/statlite-icon.png", s.handleStatliteIcon)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/summary", s.handleSummary)
	mux.HandleFunc("/api/series", s.handleSeries)
	mux.HandleFunc("/api/latest", s.handleLatest)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/monitor/status", s.handleMonitorStatus)
	mux.HandleFunc("/debug/poll-now", s.handleDebugPollNow)
	mux.HandleFunc("/debug/latest", s.handleDebugLatest)

	s.httpServer = &http.Server{
		Addr:         listen,
		Handler:      s.countRequests(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	return s
}

func monitorManagerForSingleTarget(mon *monitor.Monitor) (*monitor.Manager, error) {
	name := mon.TargetName()
	return monitor.NewManager([]monitor.ManagedTarget{{
		Metadata: monitor.TargetMetadata{Name: name},
		Monitor:  mon,
	}})
}

func (s *Server) Start() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) countRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		s.requestsTotal.Add(1)
		next.ServeHTTP(recorder, r)
		if recorder.status == http.StatusNotFound {
			s.notFoundTotal.Add(1)
		}
		if recorder.status >= 500 {
			s.serverErrors.Add(1)
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
