// Source and updates: https://github.com/PVRLabs/statlite/blob/main/docs/integrate/go/net-http.md

package main

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"runtime"
	runtimemetrics "runtime/metrics"
	"sync"
	"time"
)

const statLiteMetricsPath = "/statlite/metrics"

type statLiteRecorder struct {
	mu                  sync.Mutex
	startedAt           time.Time
	serializedStartedAt time.Time
	status              string
	previousCPUSeconds  float64
	previousCPUTime     time.Time

	requestsTotal               uint64
	responses404Total           uint64
	responses4xxTotal           uint64
	responses5xxTotal           uint64
	requestDurationSecondsTotal float64
}

func newStatLiteRecorder(startedAt time.Time, status string) *statLiteRecorder {
	if startedAt.IsZero() {
		panic("StatLite process start time must not be zero")
	}
	if status == "" {
		panic("StatLite application status must not be empty")
	}
	recorder := &statLiteRecorder{
		startedAt:           startedAt,
		serializedStartedAt: startedAt.UTC(),
		status:              status,
		previousCPUTime:     startedAt,
	}
	recorder.previousCPUSeconds = readGoCPUSeconds()
	return recorder
}

func (r *statLiteRecorder) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == statLiteMetricsPath {
			next.ServeHTTP(w, req)
			return
		}

		started := time.Now()
		writer, status := wrapResponseWriter(w)
		next.ServeHTTP(writer, req)
		if status.didHijack {
			return
		}
		r.record(status.code(), time.Since(started))
	})
}

func (r *statLiteRecorder) metricsHandler(w http.ResponseWriter, _ *http.Request) {
	snapshot := r.snapshot()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		return
	}
}

func (r *statLiteRecorder) record(status int, duration time.Duration) {
	seconds := duration.Seconds()
	if seconds < 0 {
		seconds = 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.requestsTotal++
	r.requestDurationSecondsTotal += seconds
	if status == http.StatusNotFound {
		r.responses404Total++
	}
	if status >= 400 && status < 500 {
		r.responses4xxTotal++
	}
	if status >= 500 && status < 600 {
		r.responses5xxTotal++
	}
}

type statLiteSnapshot struct {
	Schema      string          `json:"schema"`
	Integration string          `json:"integration"`
	Status      string          `json:"status"`
	StartedAt   string          `json:"started_at"`
	Metrics     statLiteMetrics `json:"metrics"`
}

type statLiteMetrics struct {
	RequestsTotal               uint64  `json:"requests_total"`
	Responses404Total           uint64  `json:"responses_404_total"`
	Responses4xxTotal           uint64  `json:"responses_4xx_total"`
	Responses5xxTotal           uint64  `json:"responses_5xx_total"`
	RequestDurationSecondsTotal float64 `json:"request_duration_seconds_total"`
	ProcessCPUUsage             float64 `json:"process_cpu_usage"`
	RuntimeHeapUsedBytes        uint64  `json:"runtime_heap_used_bytes"`
	UptimeSeconds               float64 `json:"uptime_seconds"`
}

func (r *statLiteRecorder) snapshot() statLiteSnapshot {
	now := time.Now()
	currentCPUSeconds := readGoCPUSeconds()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)

	r.mu.Lock()
	elapsed := now.Sub(r.previousCPUTime).Seconds()
	processCPUUsage := 0.0
	if elapsed >= 0 && currentCPUSeconds >= r.previousCPUSeconds {
		if elapsed > 0 {
			processCPUUsage = (currentCPUSeconds - r.previousCPUSeconds) / elapsed
		}
		r.previousCPUSeconds = currentCPUSeconds
		r.previousCPUTime = now
	}
	metrics := statLiteMetrics{
		RequestsTotal:               r.requestsTotal,
		Responses404Total:           r.responses404Total,
		Responses4xxTotal:           r.responses4xxTotal,
		Responses5xxTotal:           r.responses5xxTotal,
		RequestDurationSecondsTotal: r.requestDurationSecondsTotal,
		ProcessCPUUsage:             processCPUUsage,
		RuntimeHeapUsedBytes:        memory.Alloc,
		UptimeSeconds:               now.Sub(r.startedAt).Seconds(),
	}
	serializedStartedAt := r.serializedStartedAt
	status := r.status
	r.mu.Unlock()

	if metrics.UptimeSeconds < 0 {
		metrics.UptimeSeconds = 0
	}
	return statLiteSnapshot{
		Schema:      "statlite-metrics/v1",
		Integration: "net-http",
		Status:      status,
		StartedAt:   serializedStartedAt.Format(time.RFC3339Nano),
		Metrics:     metrics,
	}
}

func readGoCPUSeconds() float64 {
	samples := []runtimemetrics.Sample{
		{Name: "/cpu/classes/user:cpu-seconds"},
		{Name: "/cpu/classes/gc/total:cpu-seconds"},
	}
	runtimemetrics.Read(samples)
	return samples[0].Value.Float64() + samples[1].Value.Float64()
}

type responseStatusWriter struct {
	http.ResponseWriter
	finalStatus int
	didHijack   bool
}

func (w *responseStatusWriter) WriteHeader(status int) {
	if w.finalStatus != 0 {
		return
	}
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.finalStatus = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStatusWriter) Write(p []byte) (int, error) {
	if w.finalStatus == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseStatusWriter) code() int {
	if w.finalStatus == 0 {
		return http.StatusOK
	}
	return w.finalStatus
}

func (w *responseStatusWriter) flush() {
	if w.finalStatus == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *responseStatusWriter) hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, buffered, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err == nil {
		w.didHijack = true
	}
	return connection, buffered, err
}

type responseFlusher struct{ *responseStatusWriter }

func (w *responseFlusher) Flush() { w.flush() }

type responseHijacker struct{ *responseStatusWriter }

func (w *responseHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.hijack()
}

type responseFlusherHijacker struct{ *responseStatusWriter }

func (w *responseFlusherHijacker) Flush() { w.flush() }

func (w *responseFlusherHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.hijack()
}

func wrapResponseWriter(w http.ResponseWriter) (http.ResponseWriter, *responseStatusWriter) {
	status := &responseStatusWriter{ResponseWriter: w}
	_, flush := w.(http.Flusher)
	_, hijack := w.(http.Hijacker)
	switch {
	case flush && hijack:
		return &responseFlusherHijacker{status}, status
	case flush:
		return &responseFlusher{status}, status
	case hijack:
		return &responseHijacker{status}, status
	default:
		return status, status
	}
}
