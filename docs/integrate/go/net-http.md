# Integrate Go net/http with StatLite Metrics

This guide adds lightweight monitoring to a Go `net/http` application through
the fixed `statlite-metrics/v1` JSON profile and a standard-library-only
helper. A [runnable and tested demo](../../../examples/go-net-http-demo/)
contains the same helper.

## When to use this integration

Go does not have a first-class StatLite target type. Use this direct v1
integration when StatLite's fixed traffic, error, average-latency, status,
restart, and process signals fit the application's operational needs.

## Why this integration instead of a Go target?

StatLite has experimented with a first-class Go target that reads
Prometheus/OpenMetrics output. That research helped inform the current
decision to use an application-owned `statlite-metrics/v1` integration for Go
applications.

The experiment showed that Go runtime and Prometheus metrics alone do not
provide a consistent application-level contract across Go applications.
Useful HTTP request, error, and latency metrics depend on the application,
framework, and instrumentation choices, so a generic Go target would either
support only a subset of applications or require broader configuration and
detection logic.

For now, StatLite uses this application-owned `statlite-metrics/v1`
integration for Go applications. It keeps the contract explicit, small, and
predictable while providing the HTTP metrics StatLite needs.

A future first-class Go or framework-specific integration is still possible
if a sufficiently stable and useful contract emerges.

If you have a Go application or library with a stable metrics contract that
could support a useful first-class StatLite integration, [open an issue](https://github.com/PVRLabs/statlite/issues/new/choose)
or [start a GitHub Discussion](https://github.com/PVRLabs/statlite/discussions/new/choose).
We are interested in concrete integration opportunities.

This helper supports concurrent requests within one process, but its counters
are process-local. Do not poll a load-balanced URL that alternates independent
processes or replicas. Counters can decrease and `started_at` can alternate,
creating misleading deltas or apparent restarts. Use application-owned shared
aggregation or one stable endpoint and StatLite target per process.

The example was exercised with Go 1.27.1. This guide claims that tested
baseline, not compatibility with every Go release.

## Copyable helper

Save this complete helper as `statlite.go` in your application's package:

```go
// Source and updates: https://github.com/PVRLabs/statlite/blob/main/docs/integrate/go/net-http.md

package main

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"time"
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
		Integration: "net-http",
		Status:      status,
		StartedAt:   serializedStartedAt.Format(time.RFC3339Nano),
		Metrics:     metrics,
	}
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
```

The recorder locks only while updating or copying state. JSON encoding and
response writes happen after the consistent snapshot is copied.

## Register the endpoint and middleware

Capture the process start identity once during application setup and inject it
into the recorder. Rebuilding or re-registering the recorder in that process
must reuse the same value. The helper retains the original `time.Now()` value
for monotonic uptime calculations and keeps a separate UTC wall-clock copy for
the serialized `started_at` field.

```go
package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
)

func main() {
	processStartedAt := time.Now()
	recorder := newStatLiteRecorder(processStartedAt, "UP")

	mux := http.NewServeMux()
	mux.HandleFunc(statLiteMetricsPath, recorder.metricsHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		_, _ = fmt.Fprintln(w, "hello")
	})

	server := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           recorder.middleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
```

The `"UP"` value is application-provided status, not a value inferred by the
helper. It does not prove that every database or dependency is healthy. A
successful metrics response proves reporting availability. Add
`database_status` only when the application has an authoritative, inexpensive
or cached signal.

## Endpoint and response status behavior

`GET /statlite/metrics` returns the snapshot. The middleware compares
`req.URL.Path`, so a query string remains excluded from application counters
and duration.

Informational 1xx headers other than 101 are forwarded without becoming the
final recorded status. A 101 Switching Protocols response is terminal, as are
the first 2xx through 5xx `WriteHeader`, the first `Write`, or a flush before a
final header. A write or initial flush commits an implicit 200, matching
`net/http`. A handler that returns without writing is also recorded as an
implicit 200. A completed 404 increments both the 404 and 4xx counters. The
middleware records once after a handler returns.

The helper deliberately does not recover panics. Its record call is not
deferred, so an escaping panic with no completed response is not misreported as
an implicit 200. The `net/http` server's outer panic recovery cannot provide a
reliable final 500 to this inner wrapper. If an application wants recovered
panics counted, place its recovery middleware inside the StatLite middleware
and have recovery write 500 before returning:

```go
handler := recorder.middleware(applicationRecovery(mux))
```

## ResponseWriter compatibility boundary

The wrapper preserves `http.Flusher` and `http.Hijacker` only when the
underlying writer implements them. It does not falsely advertise either
capability. `Unwrap() http.ResponseWriter` lets `http.ResponseController`
reach capabilities on the underlying writer. The tests exercise flush and
hijack against a real HTTP server.

After a successful hijack, the HTTP server no longer owns the connection and
the application can exchange raw protocol data for an arbitrary lifetime. The
helper excludes that request from every StatLite HTTP counter and from total
request duration, even if a header was written first. WebSockets, raw protocol
upgrades, and other hijacked connections are outside the certified metrics
boundary.

Prefer omission over inaccurate metrics for behavior outside the normal
request/response path. The helper preserves application behavior where
practical, but does not attempt to become a general `ResponseWriter`
compatibility layer.

The copyable helper does not attempt to mirror every optional interface.
Direct type assertions for other interfaces, including `http.Pusher` and
`io.ReaderFrom`, are not guaranteed to survive the wrapper. Prefer
`http.ResponseController` where it offers the required operation. The helper
does not buffer request or response bodies.

## Configure and verify StatLite

Save this as `statlite.yaml`:

```yaml
server:
  listen: "127.0.0.1:9090"

storage:
  sqlite_path: "./statlite-go-net-http-demo.sqlite"

polling:
  interval: "10s"
  timeout: "5s"

targets:
  - name: "go-net-http-demo"
    type: "statlite-metrics"
    url: "http://127.0.0.1:8080/statlite/metrics"
```

Application integration is still required. The YAML tells StatLite where to
poll; it does not add middleware or an endpoint to the Go application.

Start the application, then generate normal and error traffic:

```bash
go run .
curl -s http://127.0.0.1:8080/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/missing
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/client-error
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/failure
curl -s http://127.0.0.1:8080/statlite/metrics
statlite inspect 'http://127.0.0.1:8080/statlite/metrics'
statlite --config statlite.yaml
```

Open <http://127.0.0.1:9090>. Use a 30-second or longer polling interval in
production.

## Deployment and optional fields

Counters, duration, uptime, and start identity belong to one process. Multiple
processes or replicas are not automatically aggregated. A multi-process
deployment needs application-owned shared aggregation or a stable endpoint
and separate StatLite target for every process. Polling one load-balanced URL
across those processes is unsupported because cumulative counters can decrease
and process identity can alternate.

The helper intentionally omits CPU, heap, host, and database fields. Every
individual metric is optional under v1. Add fields only when their values
match the specification. Do not substitute process RSS for runtime-managed
heap, and do not perform blocking dependency checks during a metrics request.

The `statlite-metrics` target does not currently send target credentials, and
`statlite inspect` does not accept authentication options. Make the endpoint
reachable through network or proxy controls that do not require StatLite to
authenticate, and restrict access because it exposes operational data.

## References

- [Runnable Go net/http demo](../../../examples/go-net-http-demo/)
- [StatLite Metrics v1 specification](../../statlite-metrics-v1.md)
- [Why StatLite Metrics?](../../why-statlite-metrics.md)
- [Integration guide index](../)
- [StatLite configuration](../../configuration.md)
- [Go net/http package](https://pkg.go.dev/net/http)
