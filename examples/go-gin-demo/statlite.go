// Source and updates: https://github.com/PVRLabs/statlite/blob/main/docs/integrate/go/gin.md

package main

import (
	"encoding/json"
	"net/http"
	"runtime"
	runtimemetrics "runtime/metrics"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
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

func (r *statLiteRecorder) middleware(c *gin.Context) {
	if c.Request.URL.Path == statLiteMetricsPath || advertisesProtocolUpgrade(c.Request) {
		c.Next()
		return
	}

	started := time.Now()
	c.Next()
	r.record(c.Writer.Status(), time.Since(started))
}

func advertisesProtocolUpgrade(request *http.Request) bool {
	if request.Header.Get("Upgrade") == "" {
		return false
	}
	for _, value := range request.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

func (r *statLiteRecorder) metricsHandler(c *gin.Context) {
	snapshot := r.snapshot()
	c.Header("Content-Type", "application/json")
	c.Status(http.StatusOK)
	if err := json.NewEncoder(c.Writer).Encode(snapshot); err != nil {
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
		Integration: "gin",
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
