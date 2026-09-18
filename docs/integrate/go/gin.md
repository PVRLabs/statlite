# Integrate Gin with StatLite Metrics

This guide adds lightweight monitoring to a Gin application through the fixed
`statlite-metrics/v1` JSON profile and Gin-native middleware. A
[runnable and tested demo](../../../examples/go-gin-demo/) contains the same
helper.

## When to use this integration

Gin does not have a first-class StatLite target type. Use this direct v1
integration when StatLite's fixed traffic, error, average-latency, status,
restart, and process signals fit the application's operational needs.

This helper supports concurrent requests and goroutines within one process,
but its counters are process-local. Do not poll a load-balanced URL that
alternates independent processes or replicas. Counters can decrease and
`started_at` can alternate, creating misleading deltas or apparent restarts.
Use application-owned shared aggregation or one stable endpoint and StatLite
target per process.

The example was exercised with Go 1.27.1 and Gin 1.12.0. This guide claims
that tested baseline, not compatibility with every Go or Gin release.

## Copyable helper

Save this complete helper as `statlite.go` in your application's package:

```go
// Source and updates: https://github.com/PVRLabs/statlite/blob/main/docs/integrate/go/gin.md

package main

import (
	"encoding/json"
	"net/http"
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
	return &statLiteRecorder{
		startedAt:           startedAt,
		serializedStartedAt: startedAt.UTC(),
		status:              status,
	}
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
	UptimeSeconds               float64 `json:"uptime_seconds"`
}

func (r *statLiteRecorder) snapshot() statLiteSnapshot {
	r.mu.Lock()
	metrics := statLiteMetrics{
		RequestsTotal:               r.requestsTotal,
		Responses404Total:           r.responses404Total,
		Responses4xxTotal:           r.responses4xxTotal,
		Responses5xxTotal:           r.responses5xxTotal,
		RequestDurationSecondsTotal: r.requestDurationSecondsTotal,
		UptimeSeconds:               time.Since(r.startedAt).Seconds(),
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
```

The recorder locks only while updating or copying state. JSON encoding and
response writes happen after the consistent snapshot is copied. The
application provides the non-empty status. A successful metrics response
proves reporting availability, not that every database or dependency is
healthy. Add `database_status` only when the application has an authoritative,
inexpensive or cached signal.

## Register recovery in the required order

Capture the process start identity once during application setup and inject it
into the recorder. Rebuilding or re-registering middleware in that process
must reuse the same value. A new process gets a new value. The helper retains
the original `time.Now()` value for monotonic uptime calculations and a UTC
wall-clock copy for `started_at`.

```go
func main() {
	processStartedAt := time.Now()
	recorder := newStatLiteRecorder(processStartedAt, "UP")

	engine := gin.New()
	engine.Use(recorder.middleware)
	engine.Use(gin.Recovery())
	engine.GET(statLiteMetricsPath, recorder.metricsHandler)
	engine.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "hello\n")
	})

	log.Fatal(engine.Run("127.0.0.1:8080"))
}
```

The order is significant. The StatLite middleware must be registered before
`gin.Recovery()` so it resumes after recovery has written a 500 for a panic
that occurred before response commitment. Do not use `gin.Default()` and then
append the StatLite middleware: `gin.Default()` installs recovery first, which
puts recovery outside middleware added later and prevents the StatLite code
after `c.Next()` from recording the recovered request.

## Status, recovery, and writer behavior

After downstream handlers and recovery return, the middleware reads
`c.Writer.Status()`. Normal responses, Gin's framework 404, other 4xx
responses, and ordinary 5xx responses therefore use Gin's final observable
status. A 404 increments both the 404 and 4xx counters. An implicit response
is 200.

The recovery claim is limited to panics before response commitment. If a
handler writes a response and then panics, recovery cannot reliably replace
the committed status. The middleware records the final status that Gin and the
server expose. It neither rewrites that response nor synthesizes a 500.

The helper never replaces or decorates `c.Writer`, so normal HTTP streaming
and flushing remain Gin's responsibility. Before routing, it excludes requests
that advertise a conventional HTTP Upgrade handshake with both a non-empty
`Upgrade` header and an `upgrade` token in `Connection`. This includes rejected
upgrade attempts that ultimately return an ordinary 400, 404, or 500. The
helper deliberately prefers that omission to timing a connection that might
become a long-lived raw protocol session or recording Gin's default status for
a handshake written after hijacking.

This request-header check does not detect arbitrary raw hijacks that omit the
conventional upgrade headers. Such hijacks are outside the certified metrics
path. Applications using them must exclude or instrument those routes
themselves. WebSocket clients normally advertise the conventional handshake
and are covered by the helper's pre-routing exclusion.

Requests whose URL path is exactly `/statlite/metrics` are excluded from all
application request counters and duration, including requests with a query
string. The endpoint returns the current inexpensive process-local snapshot.

## Configure StatLite

Configure the existing `statlite-metrics` target type, not a Gin target type:

```yaml
server:
  listen: "127.0.0.1:9090"

storage:
  sqlite_path: "./statlite.sqlite"

polling:
  interval: "30s"
  timeout: "5s"

targets:
  - name: "my-gin-app"
    type: "statlite-metrics"
    url: "http://127.0.0.1:8080/statlite/metrics"
```

The `statlite-metrics` target does not currently send target credentials, and
`statlite inspect` does not accept authentication options. Keep the endpoint
private through loopback binding, a private network, or proxy controls that do
not require StatLite to authenticate. If a proxy otherwise requires
authentication, it must separately allow the StatLite polling path. Restrict
access because the endpoint exposes operational data.

## Verify the integration

Start the application, make representative requests, then inspect the profile:

```bash
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/missing
curl -s http://127.0.0.1:8080/statlite/metrics
statlite inspect 'http://127.0.0.1:8080/statlite/metrics'
statlite --config statlite.yaml
```

The profile's counters are cumulative for one process. StatLite derives rates,
counter deltas, and average request latency from successive collections.
Restarting the application resets counters and changes `started_at`.

## Deployment limitation

Concurrent requests and goroutines within one process are supported. Separate
processes or replicas have independent in-memory counters, and StatLite does
not automatically aggregate them. A multi-process deployment requires
application-owned shared aggregation or a stable endpoint and separate
StatLite target for every process.

Polling one load-balanced URL across independent processes is unsupported.
Cumulative counters can decrease and process start identity can alternate,
producing misleading deltas or apparent restarts.

See the [runnable Gin demo](../../../examples/go-gin-demo/) for the complete
routes, configuration, and framework-level tests.
