# Metric vocabulary

This reference describes the fixed metrics StatLite currently collects, stores,
and derives. Availability depends on the integration and the fields the source
publishes. The [integration guides](integrations.md) describe supported setups;
[StatLite Metrics v1](statlite-metrics-v1.md) defines the producer JSON contract,
and [API v1](api.md) defines the supported external read API.

## Names and surfaces

A producer field, normalized sample, chart value, and public API field can have
different names or units. For example, v1 `requests_total` becomes a stored
`http_requests_total` counter, then a query-time request delta, exposed as
`requests`. A normalized sample contains a `key`, `kind` (`counter` or `gauge`),
`value`, and optional `unit`. SQLite retains these raw normalized observations
as the authoritative history for each poll.

Internal dashboard/debug routes can expose normalized samples. They are not
part of the supported external API contract, but existing names are retained.

## Current normalized samples

Units in this table are the strings emitted by current adapters. A historical
sample can lack a unit. Missing observations are unavailable, not zero.

| Sample key | Kind | Unit | Meaning and operational use |
| --- | --- | --- | --- |
| `http_requests_total` | counter | `requests` | Cumulative completed HTTP requests; deltas show observed request volume. |
| `http_404_total` | counter | `requests` | Cumulative HTTP 404 responses; helps identify missing resources or incorrect paths. |
| `http_4xx_total` | counter | `requests` | Cumulative HTTP 400–499 responses, including 404; helps identify client/request failures. |
| `http_5xx_total` | counter | `requests` | Cumulative HTTP 500–599 responses; helps identify server failures. |
| `http_request_time_total_seconds` | counter | `seconds` | Cumulative request duration; paired request-count deltas yield average latency. This is not elapsed wall time or a maximum latency. |
| `process_cpu_usage` | gauge | `ratio` for JVM adapters; `cores` for v1 | Process CPU consumption. The unit depends on the source; see CPU interpretation below. |
| `jvm_heap_used_bytes` | gauge | `bytes` | JVM heap currently used, summed over heap pools; helps track heap pressure and growth. |
| `runtime_heap_used_bytes` | gauge | `bytes` | Producer-defined runtime-managed memory currently used; helps track memory trends within the same runtime and instrumentation. |
| `process_start_time` | gauge | `unix_seconds` | Process start as Unix seconds, potentially fractional; also sets process-start metadata used to identify restarts. |
| `process_uptime` | gauge | `seconds` | Process uptime where supplied; a decrease is a restart-detection signal. It is stored but is not a chart series. |
| `host_cpu_usage` | gauge | `ratio` | Fraction of visible host CPU capacity in `[0, 1]`; helps identify host CPU pressure. |
| `host_memory_used_bytes` | gauge | `bytes` | Used memory visible to the producer; used with total memory to derive utilization. |
| `host_memory_total_bytes` | gauge | `bytes` | Total memory visible to the producer. |
| `host_disk_used_bytes` | gauge | `bytes` | Used capacity of the selected filesystem; used with total capacity to track space pressure. |
| `host_disk_total_bytes` | gauge | `bytes` | Total capacity of that filesystem. |

Health status, database health, poll status, and collector warnings/errors are
separate signals, not numeric metric samples. Successful collection does not
create an application-health measurement when the source provides none.

## Source mappings and availability

### StatLite Metrics v1

All numeric fields below are optional. An omitted field does not become zero.
The same mappings apply to documented application producers and StatLite's own
`/statlite/metrics` endpoint when collected as a target.

| Producer field | Normalized sample |
| --- | --- |
| `metrics.requests_total` | `http_requests_total` |
| `metrics.responses_404_total` | `http_404_total` |
| `metrics.responses_4xx_total` | `http_4xx_total` |
| `metrics.responses_5xx_total` | `http_5xx_total` |
| `metrics.request_duration_seconds_total` | `http_request_time_total_seconds` |
| `metrics.process_cpu_usage` | `process_cpu_usage`, unit `cores` |
| `metrics.runtime_heap_used_bytes` | `runtime_heap_used_bytes` |
| `metrics.uptime_seconds` | `process_uptime` |
| `metrics.host_cpu_usage` | `host_cpu_usage` |
| `metrics.host_memory_used_bytes`, `metrics.host_memory_total_bytes` | Same keys |
| `metrics.host_disk_used_bytes`, `metrics.host_disk_total_bytes` | Same keys |
| `started_at` | `process_start_time` and process-start metadata |

`metrics.request_duration_seconds_max` is accepted for producer compatibility
but ignored by collection. It produces no normalized sample, stored metric, or
chart. Application and database status fields become separate health metadata.

### Spring Boot Actuator

The metrics endpoint uses `http.server.requests` COUNT for request totals,
TOTAL_TIME for duration totals, and status-filtered COUNT measurements for
404/4xx/5xx totals. Runtime mappings are:

| Actuator source | Normalized sample |
| --- | --- |
| `jvm.memory.used`, filtered to `area:heap`, VALUE | `jvm_heap_used_bytes` |
| `process.cpu.usage`, VALUE | `process_cpu_usage`, unit `ratio` |
| `process.start.time`, VALUE | `process_start_time` and process-start metadata |
| `system.cpu.usage`, VALUE | `host_cpu_usage`, only with host collection enabled |
| `disk.total` minus `disk.free`, VALUE | `host_disk_used_bytes`, only with host collection enabled |
| `disk.total`, VALUE | `host_disk_total_bytes`, only with host collection enabled |

### JVM Prometheus endpoints

Spring Boot's Prometheus source and the explicit Quarkus and Micronaut adapters
consume a bounded set of families, not arbitrary Prometheus metrics.

| Source family | Normalized sample |
| --- | --- |
| `http_server_requests_seconds_count` | `http_requests_total` plus status-derived 404/4xx/5xx counters |
| `http_server_requests_seconds_sum` | `http_request_time_total_seconds` |
| `jvm_memory_used_bytes{area="heap"}` | `jvm_heap_used_bytes`, summed over heap pools |
| `process_cpu_usage` | `process_cpu_usage`, unit `ratio` |
| `process_start_time_seconds` | `process_start_time` and process-start metadata |
| `process_uptime_seconds` | `process_uptime`, Quarkus and Micronaut only |
| `system_cpu_usage` | `host_cpu_usage`, Spring only with host collection enabled |
| `disk_total_bytes` minus `disk_free_bytes` | `host_disk_used_bytes`, Spring only with host collection enabled |
| `disk_total_bytes` | `host_disk_total_bytes`, Spring only with host collection enabled |

Spring does not collect host memory or uptime through these adapters.
Quarkus and Micronaut do not infer host metrics. Their adapters validate required
HTTP labels and matching count/sum identities before emitting duration totals;
Spring has its own source-specific normalization rules. See the
[Quarkus](targets/quarkus.md), [Micronaut](targets/micronaut.md), and
[Spring Boot](targets/spring.md) guides for integration behavior. HTTP meters
may be absent before requests are recorded. Source labels and histogram buckets
are not retained as arbitrary query dimensions.

## CPU interpretation and chart units

JVM `process_cpu_usage` is a ratio in `[0, 1]`. A ratio of `0.25` represents
25% of the capacity used by the source's denominator. StatLite does not discover
or change that denominator.

StatLite Metrics v1 uses `process_cpu_usage` for logical CPU equivalents: CPU
time consumed per elapsed wall time. `0.25` means one quarter of a logical CPU;
`2.75` means 2.75 logical CPUs. These values are not fractions of total capacity.
Converting them to a capacity ratio requires a known denominator that StatLite
does not collect.

Each target's CPU chart uses one unit based on its configured integration:
JVM/Micrometer targets display normalized utilization as a percentage; StatLite
Metrics v1 targets display logical CPU consumption in cores. For example, a
JVM ratio of `0.25` displays as `25%`, and v1 consumption of `2.75` displays as
`2.75 cores`. The existing CPU axis and tooltip use that unit for the whole chart.

Stored CPU samples whose explicit unit conflicts with the chart unit are
excluded before chart averaging, with a notice. Other metrics remain available.
Samples without a stored unit use the configured integration's expected unit;
a notice identifies that assumption. This preserves ordinary historical JVM
charts and interprets ordinary v1 history in cores. It cannot establish the
original meaning of missing-unit samples if a target changed representation.
Targets switching CPU representations are not rendered as mixed-unit charts.
Long-range charts retain their existing endpoint sampling; notices describe the
CPU observations read for that chart, including its native latest point.

The public API keeps its existing values and unit-agnostic aggregation for
compatibility. See the [API CPU field description](api.md) for that contract.

## Runtime memory interpretation

The derived `heap_used_bytes` series prefers a gauge named
`runtime_heap_used_bytes` in each poll and otherwise falls back to a
`jvm_heap_used_bytes` gauge. The public API names this value
`runtime_memory_bytes`. It does not add the two samples together.

For JVM adapters this is heap used. For Go producers it can be allocated Go
heap; Node and Python producers have their own documented runtime measurements.
These measurements are useful for trends within a consistent setup. They do
not measure whole-process RSS, memory limits, maximum heap size, or comparable
cross-runtime memory consumption.

Host memory and disk values describe the execution environment and filesystem
visible to the source. Containers can expose a different scope from the physical
host. Host measurements are lightweight operational estimates.

## Derived series and display values

Raw counters remain cumulative in storage. Queries calculate nonnegative
deltas from earlier samples in the same application run. Missing baselines,
resets, or run changes leave a delta unavailable. Average latency requires
positive request volume and matching request-count/duration baselines. A zero
request delta therefore has no average latency. Missing samples and failed polls
do not fabricate measurements.

| Derived concept | Internal series field | Display or public API representation |
| --- | --- | --- |
| Request volume | `requests` | Counter delta; public minute buckets sum available deltas. |
| Response errors | `http_404`, `http_4xx`, `http_5xx` | Counter deltas; API v1 exposes 4xx/5xx counts with paired-coverage rules. |
| Mean request latency | `average_latency_seconds` | Dashboard milliseconds; API `avg_latency_ms`, with `latency_requests` indicating covered requests. |
| Runtime memory | `heap_used_bytes` | Dashboard bytes / 1024² (labeled MB); API `runtime_memory_bytes` in bytes. |
| Process CPU | `process_cpu_usage` | Dashboard percentage for JVM ratios or cores for v1; see chart-unit rules and the API field description. |
| Host CPU | `host_cpu_usage` | Ratio in series/API; percentage on dashboard. |
| Host memory | `host_memory_used_bytes`, `host_memory_total_bytes`, `host_memory_usage` | Usage ratio requires a valid pair with `0 <= used <= total` and `total > 0`. API exposes the usage ratio. |
| Host disk | `host_disk_used_bytes`, `host_disk_total_bytes`, `host_disk_usage` | Same pair validation; API exposes the usage ratio. |

Chart downsampling sums counter deltas, averages available gauges, and weights
latency by its matching requests. API v1 groups observations into occupied UTC
minute slots with its documented coverage rules. Neither operation creates
missing observations. A long-gap counter delta belongs to the end poll's slot;
it is not distributed over unobserved time. See [API v1](api.md) for exact field,
coverage, and freshness semantics.
