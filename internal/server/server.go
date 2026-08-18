package server

// This file owns HTTP server construction, route registration, and lifecycle.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pvrlabs/statlite/internal/collector"
	"github.com/pvrlabs/statlite/internal/dashboard"
	"github.com/pvrlabs/statlite/internal/monitor"
)

type hostSampler interface {
	Sample(filesystemPath string) (collector.HostMetrics, []error)
}

const resourceSnapshotInterval = 15 * time.Second

type resourceSnapshot struct {
	processHeap uint64
	processCPU  float64
	host        collector.HostMetrics
}

type Server struct {
	httpServer        *http.Server
	manager           *monitor.Manager
	retentionDays     int
	retentionCutoff   func() time.Time
	startedAt         time.Time
	requestsTotal     atomic.Uint64
	notFoundTotal     atomic.Uint64
	clientErrors      atomic.Uint64
	serverErrors      atomic.Uint64
	durationTotalNS   atomic.Uint64
	durationMaxNS     atomic.Uint64
	filesystemPath    string
	hostSampler       hostSampler
	resourceMu        sync.Mutex
	resourceSnapshot  resourceSnapshot
	resourceSampledAt time.Time
	resourceInterval  time.Duration
	storageHealthMu   sync.RWMutex
	storageHealth     string
	storageCancel     context.CancelFunc
	storageHealthy    func(context.Context) bool
	storageAvailable  func() bool
	storageInterval   time.Duration
	cpuMu             sync.Mutex
	lastCPUAt         time.Time
	lastCPUSeconds    float64
	lastCPUUsage      float64
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
	return NewWithManagerRetentionCutoffAndFilesystem(listen, manager, retentionDays, retentionCutoff, "")
}

func NewWithManagerRetentionCutoffAndFilesystem(listen string, manager *monitor.Manager, retentionDays int, retentionCutoff func() time.Time, filesystemPath string) *Server {
	mux := http.NewServeMux()
	if retentionDays > 0 && retentionCutoff == nil {
		retentionCutoff = func() time.Time {
			return time.Now().UTC().AddDate(0, 0, -retentionDays)
		}
	}
	s := &Server{
		manager:          manager,
		retentionDays:    retentionDays,
		retentionCutoff:  retentionCutoff,
		startedAt:        time.Now().UTC(),
		filesystemPath:   filesystemPath,
		hostSampler:      collector.NewHostSampler(),
		resourceInterval: resourceSnapshotInterval,
		storageHealth:    initialStorageHealth(manager),
		storageInterval:  storageHealthCheckInterval,
	}
	if manager != nil {
		storageMonitor := manager.PrimaryMonitor()
		s.storageHealthy = storageMonitor.StorageHealthy
		s.storageAvailable = storageMonitor.StorageAvailable
	}

	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc(dashboard.ScriptPath(), s.handleDashboardScript)
	mux.HandleFunc("/static/statlite-icon.png", s.handleStatliteIcon)
	mux.HandleFunc(dashboard.ChartJSPath, s.handleDashboardVendor)
	mux.HandleFunc(dashboard.OrbitronFontPath, s.handleDashboardVendor)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/statlite/metrics", s.handleStatliteMetrics)
	mux.HandleFunc("/api/summary", s.handleSummary)
	mux.HandleFunc("/api/series", s.handleSeries)
	mux.HandleFunc("/api/latest", s.handleLatest)
	mux.HandleFunc("/api/events", s.handleEvents)
	mux.HandleFunc("/api/monitor/status", s.handleMonitorStatus)
	mux.HandleFunc("/debug/poll-now", s.handleDebugPollNow)
	mux.HandleFunc("/debug/latest", s.handleLatest)

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
	listener, err := s.Listen()
	if err != nil {
		return err
	}
	return s.Serve(listener)
}

// Listen binds the configured address without starting monitor or HTTP
// serving work. Callers that need startup ordering can bind the listener,
// start Serve, and only then launch dependent background work.
func (s *Server) Listen() (net.Listener, error) {
	return net.Listen("tcp", s.httpServer.Addr)
}

// Serve runs the HTTP server on a listener previously returned by Listen.
func (s *Server) Serve(listener net.Listener) error {
	if listener == nil {
		return fmt.Errorf("server listener is nil")
	}
	s.startStorageHealthChecks()
	err := s.httpServer.Serve(listener)
	s.stopStorageHealthChecks()
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.stopStorageHealthChecks()
	return s.httpServer.Shutdown(ctx)
}

func initialStorageHealth(manager *monitor.Manager) string {
	if manager == nil {
		return "unavailable"
	}
	return "unknown"
}

func (s *Server) countRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/statlite/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		s.requestsTotal.Add(1)
		next.ServeHTTP(recorder, r)
		duration := uint64(time.Since(started))
		s.durationTotalNS.Add(duration)
		updateAtomicMax(&s.durationMaxNS, duration)
		if recorder.status == http.StatusNotFound {
			s.notFoundTotal.Add(1)
		}
		if recorder.status >= 400 && recorder.status < 500 {
			s.clientErrors.Add(1)
		}
		if recorder.status >= 500 {
			s.serverErrors.Add(1)
		}
	})
}

func updateAtomicMax(value *atomic.Uint64, candidate uint64) {
	for current := value.Load(); candidate > current; current = value.Load() {
		if value.CompareAndSwap(current, candidate) {
			return
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}
