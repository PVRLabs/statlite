# Product and architecture

## Product purpose

StatLite is a tiny self-hosted metrics dashboard for small applications and
VPS deployments, with Spring Boot Actuator as its primary application
integration. It also supports the fixed
[`statlite-metrics/v1`](statlite-metrics-v1.md) application profile, and
StatLite self-monitoring through the canonical `statlite-metrics/v1` profile.

It is intended for solo developers and small teams that need practical
production visibility without operating Prometheus and Grafana. StatLite is a
focused production-support tool, not a general observability platform.

## Product principles

StatLite prioritizes:

* a small, understandable product;
* low steady-state memory and CPU usage;
* explicit, maintainable implementation over framework-heavy abstraction;
* local SQLite storage;
* a small, closed set of supported runtime and framework targets rather than
  arbitrary metrics;
* a clean normalized boundary between collectors and the rest of the system;
* a simple, collector-neutral dashboard;
* systemd-friendly deployment; and
* practical production support over feature breadth.

The product is one small binary with simple YAML configuration, local history,
fixed integration boundaries, and a local-first dashboard.

StatLite must remain inexpensive enough to run on the small applications and
servers it monitors. New dependencies, background work, and sampling behavior
should be evaluated for their memory, CPU, disk, and network cost. Queries and
API responses should remain bounded as retained history grows. Production
defaults should favor a small steady-state footprint; faster local-demo
settings must be clearly labeled. Monitoring should not materially change the
workload being observed.

StatLite should also keep its total normalized metric vocabulary small. New
integrations should reuse existing concepts wherever possible and should not
add or retain metrics merely because an upstream ecosystem exposes them. Every
normalized metric must have a concrete dashboard, health, diagnostic, or
internal operational purpose. The core product favors low overhead, simple
semantics, and actionable defaults over comprehensive observability. Analytical
capabilities such as latency distributions and percentiles, high-cardinality
dimensions, arbitrary metrics or queries, richer aggregation, and deeper
historical analysis are outside the lightweight core and should be introduced
only in response to a clear product need, potentially using a more capable
architecture or tier. Proposals for new normalized metrics still require
explicit product and architecture review covering operational value, storage
and retention cost, dashboard behavior, and long-term compatibility.

## Supported integration boundaries

### Currently supported targets

StatLite has three supported target types:

* `spring`: Spring Boot Actuator and a fixed set of Micrometer metrics. This
  is the default target type when `type` is omitted.
* `statlite-metrics`: the canonical fixed `statlite-metrics/v1` integration
  for StatLite and external profile producers. See the
  [producer-facing specification](statlite-metrics-v1.md) for its endpoint
  contract.
* `quarkus`: Quarkus Micrometer Prometheus/OpenMetrics metrics at an exact
  configured exposition endpoint. It normalizes a fixed request, latency,
  process, heap, and restart vocabulary.
Spring, Quarkus, and StatLite Metrics are application integrations. None of
these boundaries is an arbitrary metrics API.

### Framework-first integration model

StatLite's public integration surface follows application runtimes and
frameworks rather than telemetry standards. Target types represent
technologies developers recognize and run, such as Spring Boot, Quarkus, Go,
Gin, and Fiber. Only the target types listed as currently supported above are
available today; other names describe product direction until their contracts
are implemented and certified.

Each target type owns its supported application setup, configuration and
connection behavior, compatibility inspection, normalization contract,
certification fixtures, documentation, and user-facing explanation. A valid
metrics endpoint alone is not a StatLite integration. StatLite must know which
application concepts the endpoint represents and how they can be normalized
safely.

Prometheus/OpenMetrics, Micrometer, and similar metric technologies may be
shared internally by multiple target adapters. They are implementation
plumbing, not sufficient user-facing compatibility contracts. StatLite should
not expose a generic target merely because it can parse that target's wire
format, and it should not guess the semantics of arbitrary metric names or
middleware.

`statlite-metrics` remains a deliberate exception: it is StatLite's own small,
fixed producer contract for applications that choose to integrate directly. It
does not make third-party metric standards or arbitrary producer schemas into
public target contracts.

## Deployment topology

For a collocated deployment, configure application targets (`spring`, `quarkus`,
or `statlite-metrics`) for application and process data, and `statlite-self`
through `/statlite/metrics` to monitor StatLite itself. The self response also
provides the local host CPU, memory, and SQLite-filesystem disk capacity, so
one target presents StatLite's application, process, and host resources.

Host fields in `statlite-metrics/v1` remain optional for applications that
deliberately expose the execution environment visible to their process. Those
values stay attached to the application target and can appear beside its
application and process charts; StatLite does not create a separate host
target.

A central StatLite instance cannot infer host resources for a remote
application. For remote host visibility, the application must emit optional
host fields, or another StatLite instance must run on that host.

## Resource profile

StatLite is designed for resource-constrained deployments, including small VPS
instances. Current macOS and Linux measurements show about 15 MiB of idle RSS
for the standalone StatLite process. This is an observed idle baseline, not a
maximum-memory guarantee or SLA. Memory usage can increase temporarily during
intensive historical queries.

## Normalized collection model

Collectors keep source-specific response formats inside their adapters. The
rest of the system operates on normalized concepts such as:

* application health;
* dependency or database health when available;
* process start time and uptime;
* restart signals;
* request, `404`, `4xx`, and `5xx` counters;
* accumulated request time for deriving average latency;
* runtime memory;
* CPU usage;
* poll status; and
* collector warnings and errors.

Source-specific metric names should not leak into storage or primary dashboard
paths. For example, Actuator and StatLite Metrics fields are mapped to shared
internal concepts such as `http_requests_total`, `http_4xx_total`,
`http_request_time_total_seconds`, `process_cpu_usage`, and
`runtime_heap_used_bytes`. Those names are adapter and normalized-model
details, not additional producer protocols.

The dashboard should remain coherent across collector types. If a collector
cannot provide a concept, it leaves that concept unavailable and records a
warning when the missing data is useful for diagnosis.

## Poll and storage model

One polling cycle produces one logical collection result. StatLite stores one
poll/snapshot record for that cycle, with all metric samples observed together
linked to the same poll. Collector warnings and errors are associated with the
poll as events.

Metric samples are stored as raw normalized values. Raw samples are the source
of truth. Derived counter deltas are computed at query time, and negative
counter deltas are never displayed.

The durable model therefore separates:

* target identity and configuration;
* detected application runs;
* one poll record per collection cycle;
* raw counter and gauge samples linked to that poll; and
* warning and error events linked to that poll.

This is a normalized internal collection model, not a general scrape format or
query language.

## Counters, gauges, and derived values

Counters are cumulative values that normally reset when the monitored process
restarts. Gauges are sampled directly at each poll. Request and error charts
use query-time deltas from cumulative counters.

Average latency is derived from the request-time counter delta divided by the
request-count counter delta for the same interval. If either delta is missing
or unusable, the average remains absent rather than becoming zero. Gauges such
as runtime memory and CPU are not delta-computed.

Chart-scale aggregation, where used for longer ranges, is also derived from
the query result; it does not replace the raw stored samples.

## Restart and reset behavior

Process start time is the strongest restart signal when a collector provides
it. StatLite also considers a decrease in process uptime and decreases in
multiple core cumulative counters. A poll failure followed by a lower core
counter is another fallback signal.

Each successful collection is associated with a detected application run when
possible. Counter deltas do not cross detected process runs, so the first
sample after a detected restart does not produce a misleading delta.

An isolated counter decrease may be a metric-level reset or anomaly rather
than a full process restart. In that case the affected delta is omitted while
unaffected counters can continue to be evaluated. Negative deltas are never
shown.

Restart detection occurs during polling, not in real time. A restart between
polls may therefore be detected on the next successful poll and can lag by
approximately one polling interval. The dashboard can distinguish the
application process start time, when available, from the time StatLite records
the restart event.

## Partial collection behavior

Missing optional metrics do not fail an otherwise useful poll. If an optional
value is invalid, valid samples remain available and the collector records a
warning. Complete target fetch failures, such as an unreachable target or a
failed required HTTP request, are different from optional metric omissions and
are recorded as poll errors.

Warnings and errors remain visible through the stored collector events and
dashboard views. Missing, stale, or unavailable data should be shown clearly
rather than silently represented as zero.

## Dashboard scope

The dashboard presents a small set of normalized concepts:

* application and dependency health;
* uptime and detected restarts;
* polling status, including recent successes and failures;
* requests and HTTP errors;
* average latency;
* runtime memory;
* CPU usage; and
* recent collector events.

The supported dashboard ranges are the last hour, last 24 hours, 7 days, and 30 days.
The dashboard is intentionally simple, local-first, and collector-neutral.

## Non-goals

The current product scope does not include:

* a generic Prometheus or OpenMetrics target and arbitrary scrape ingestion;
* arbitrary metric definitions;
* labels or a custom dashboard builder;
* logs or traces;
* a Kubernetes-first architecture;
* distributed storage;
* a general-purpose query language;
* a plugin system; or
* a full alert-management platform.

These boundaries keep StatLite useful as a small production-support tool
without turning it into a general observability platform.

## Future direction

Future extensions may include additional focused runtime and framework
integrations, optional alerting, or rebuildable query acceleration if real
usage justifies them. Such possibilities are non-binding until implemented and
certified; concrete work belongs in GitHub issues and must preserve the
product's small, understandable core.

The planned Go ecosystem direction begins with a narrow `go` target for
reliably recognizable Go runtime and process concepts. Generic Go does not
imply application HTTP traffic, error, or latency support. Gin and Fiber are
the first intended framework-specific Go targets and may extend the Go
baseline with HTTP concepts only after their recommended instrumentation
contracts are investigated and certified.

`go`, `gin`, and `fiber` are not currently supported target types. Their planned
adapters may reuse bounded Prometheus/OpenMetrics parsing, but this direction
does not imply generic Prometheus compatibility or authorize arbitrary metric
ingestion.
