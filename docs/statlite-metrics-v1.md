# StatLite Metrics v1

`statlite-metrics/v1` is a fixed, language-neutral application, process, and
optional host-resource metrics profile for small integrations. StatLite uses
this same profile for its own self-monitoring. It is not a general-purpose
metrics protocol: it has no labels, arbitrary metric definitions, or
Prometheus/OpenMetrics syntax.

## Endpoint contract

An application exposes a `GET` endpoint that returns JSON with a successful
2xx HTTP status. The endpoint should produce an inexpensive, process-local
snapshot and should not perform application work such as a database query just
to serve metrics. StatLite exposes this canonical profile at
`/statlite/metrics`; its `/healthz` endpoint is readiness-only and is not a
profile endpoint.

A complete response looks like this:

```json
{
  "schema": "statlite-metrics/v1",
  "status": "UP",
  "database_status": "UP",
  "started_at": "2026-07-27T19:00:00Z",
  "metrics": {
    "requests_total": 1420,
    "responses_404_total": 18,
    "responses_4xx_total": 31,
    "responses_5xx_total": 4,
    "request_duration_seconds_total": 84.31,
    "process_cpu_usage": 0.031,
    "runtime_heap_used_bytes": 25165824,
    "uptime_seconds": 1820,
    "host_cpu_usage": 0.19,
    "host_memory_used_bytes": 1610612736,
    "host_memory_total_bytes": 4294967296,
    "host_disk_used_bytes": 19327352832,
    "host_disk_total_bytes": 42949672960
  }
}
```

The minimal valid response is:

```json
{
  "schema": "statlite-metrics/v1",
  "status": "UP"
}
```

`schema` and a non-empty `status` are required. `started_at`, `metrics`, and
every individual metric are optional. `status` is application-defined; v1 does
not impose a status enum. `started_at` uses RFC 3339 and is recommended because
it improves restart detection.

## Python and FastAPI

Request metrics must be measured inside the application, where requests and
responses pass through its middleware. The [FastAPI integration example](../examples/python-fastapi-demo/)
contains a copyable helper, FastAPI middleware registration, endpoint code, and
StatLite target configuration. Only `schema` and `status` are required; all
individual metrics are optional.

## Fields

| Field | Type | Unit | Optional | Semantics |
|---|---|---|---|---|
| `schema` | string | N/A | No | Must be `statlite-metrics/v1`. |
| `status` | string | N/A | No | Non-empty application health/status text. |
| `database_status` | string | N/A | Yes | Non-empty status text for an application database dependency when the producer can safely determine it. StatLite self-monitoring emits `UP` or `DOWN` from a cached SQLite `PingContext` check, refreshed on startup and every 60 seconds; a closed local store reports `DOWN` immediately. |
| `started_at` | string | RFC 3339 timestamp | Yes | Process start time; recommended for restart detection. |
| `metrics` | object | N/A | Yes | Container for the fixed application metric fields below. |
| `metrics.requests_total` | number | requests | Yes | Monotonic request counter. |
| `metrics.responses_404_total` | number | requests | Yes | Monotonic counter of HTTP 404 responses. |
| `metrics.responses_4xx_total` | number | requests | Yes | Monotonic counter of HTTP 4xx responses, including 404 responses. |
| `metrics.responses_5xx_total` | number | requests | Yes | Monotonic counter of HTTP 5xx responses. |
| `metrics.request_duration_seconds_total` | number | seconds | Yes | Monotonic total request duration. |
| `metrics.request_duration_seconds_max` | number | seconds | Yes | Compatibility field. StatLite accepts and ignores this value; it is not normalized, persisted, displayed, or used for dashboard calculations. Producers may continue sending it during v1. |
| `metrics.process_cpu_usage` | number | CPU cores | Yes | Process CPU consumption expressed as CPU cores; it may exceed `1.0`. |
| `metrics.runtime_heap_used_bytes` | number | bytes | Yes | Memory currently managed by the language runtime, not total process RSS and not a maximum-memory value. |
| `metrics.uptime_seconds` | number | seconds | Yes | Process uptime. StatLite persists this as the internal `process_uptime` restart-detection signal; it is not a dashboard time-series metric. |
| `metrics.host_cpu_usage` | number | ratio | Yes | Current host CPU use as a fraction of visible capacity in `[0.0, 1.0]`. |
| `metrics.host_memory_used_bytes` | number | bytes | Yes | Used memory visible to the producer. Must not exceed `host_memory_total_bytes` when both are present. |
| `metrics.host_memory_total_bytes` | number | bytes | Yes | Total memory visible to the producer. |
| `metrics.host_disk_used_bytes` | number | bytes | Yes | Used capacity for one producer-selected relevant filesystem. Must not exceed `host_disk_total_bytes` when both are present. |
| `metrics.host_disk_total_bytes` | number | bytes | Yes | Total capacity for the same selected filesystem as `host_disk_used_bytes`. |

### Counters

`requests_total`, `responses_404_total`, `responses_4xx_total`,
`responses_5xx_total`, and `request_duration_seconds_total` are monotonic
counters. They normally reset only when the process restarts. StatLite computes
counter deltas when querying history and does not display negative deltas.

### Gauges

`process_cpu_usage`, `runtime_heap_used_bytes`, `uptime_seconds`, and all host
fields are gauges. Their values describe the current observation of the
process or execution environment rather than a cumulative total.

StatLite accepts `request_duration_seconds_max` for v1 producer compatibility,
but currently ignores it. It is not part of StatLite's normalized or dashboard
metric set. Average latency is derived from request-duration-total and
request-count counter deltas; percentile and maximum-latency analysis are
outside the lightweight core.

## Host resource guidance

Host values are lightweight operational estimates, not accounting-grade
measurements. Producers should prefer simple, dependency-light collection and
omit unavailable values rather than adding complex platform-specific behavior.
In containers, fields describe the execution environment visible to the
producer on a best-effort basis; the profile does not promise precise handling
of every cgroup configuration.

StatLite derives memory and disk percentages only when each pair is present,
valid, and has a positive total. Producers must not send percentage fields.

Disk capacity describes one relevant filesystem, not every disk, device,
volume, or mount. StatLite self-monitoring selects the filesystem containing
its SQLite database. A simple application may select its working directory;
an application with a dedicated data directory should select that instead. Do
not expose filesystem paths, mount names, device labels, arrays, or per-volume
metrics. Disk I/O counters are intentionally outside v1 for now.

## Producer guidance

* Collect HTTP counters in middleware so all relevant requests and responses
  are counted consistently.
* Expose current runtime and process values.
* Do not perform blocking application database checks or other blocking I/O just to serve the metrics endpoint. Run bounded checks on a schedule or through an existing readiness mechanism, cache the result, and serve that cached status. StatLite refreshes its SQLite `database_status` on startup and every 60 seconds rather than during a metrics scrape.
* Take a consistent snapshot of related values when practical.

Host fields are optional. Spring Boot, Python, Go, and other application
integrations do not need to implement host sampling. A producer may include
them when it deliberately exposes the execution environment visible to its
process; StatLite displays those values with that application target rather
than creating a separate target.

The FastAPI helper is illustrative; this document remains the canonical
contract.

The configured StatLite target name is authoritative. The application should
not provide `target_name`, polling timestamps, or other StatLite-owned metadata.
Unknown fields are ignored for forward compatibility. Invalid optional fields
are skipped and reported as warnings without discarding otherwise valid metrics.

## Multiple processes

In-memory helpers report metrics for one process or worker. Applications using
multiple workers must either aggregate metrics themselves or expose each
process separately. StatLite does not imply that it aggregates application
workers.
