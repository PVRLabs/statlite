# Micronaut target

Configure the exact Micronaut Prometheus endpoint with explicit
`type: "micronaut"`. Management and Micrometer configuration is required.

See [Configuration](../configuration.md) for shared server, storage, polling,
and [Basic Auth](../configuration.md#basic-auth) settings. Add the target entry
to the `targets` list in your configuration.

## Basic configuration

Run the [Micronaut demo](../../examples/micronaut-metrics-demo/) for a complete
application, management configuration, and deterministic traffic recipe.

```yaml
targets:
  - name: "orders"
    type: "micronaut"
    url: "http://localhost:8080/prometheus"
```

## Application-side Micrometer setup

Include these dependencies in the application:

```xml
<dependency>
  <groupId>io.micronaut</groupId>
  <artifactId>micronaut-management</artifactId>
</dependency>
<dependency>
  <groupId>io.micronaut.micrometer</groupId>
  <artifactId>micronaut-micrometer-core</artifactId>
</dependency>
<dependency>
  <groupId>io.micronaut.micrometer</groupId>
  <artifactId>micronaut-micrometer-registry-prometheus</artifactId>
</dependency>
```

Enable metrics and permit access to the Prometheus endpoint in the application's
configuration. The minimal certified graph uses properties:

```properties
micronaut.metrics.enabled=true
endpoints.prometheus.sensitive=false
```

## Tested versions

Both setups below are supported. The primary tested graph uses platform parent
5.2.1, Micronaut core 5.2.11,
Micronaut Micrometer 6.1.0, and Micrometer 1.17.1. The same adapter also passes
the retained 4.9.9 regression (parent 4.9.2, Micronaut Micrometer 5.12.0,
Micrometer 1.15.0). The public demo and CI retain the 4.9.9 / Java 21 setup.

Tested with Temurin 25.0.4.1+1-LTS, using Java 25 bytecode for 5.x and Java 17
for 4.9.9. Compatibility covers only the tested runtime and Micrometer setups. No Prometheus server or Grafana is required.
Removing management from this graph removes both `/prometheus` and `/health`.
To retain metrics with absent health, keep management installed and set
`endpoints.health.enabled=false`.

## Metrics StatLite consumes

Only existing StatLite concepts are normalized:

| StatLite sample | Micronaut source |
| --- | --- |
| `http_requests_total` | Sum `http_server_requests_seconds_count` |
| `http_404_total` | Count with exact status 404 |
| `http_4xx_total` | Count with status 400–499, including 404 |
| `http_5xx_total` | Count with status 500–599 |
| `http_request_time_total_seconds` | Sum `_sum` with matching accepted count/sum identities |
| `process_cpu_usage` | Finite `process_cpu_usage` ratio from 0 to 1 |
| `jvm_heap_used_bytes` | Sum nonnegative `jvm_memory_used_bytes{area="heap"}` |
| `process_start_time` | Valid `process_start_time_seconds`, also used for restart identity |
| `process_uptime` | Finite nonnegative `process_uptime_seconds` |

Both HTTP count and sum require nonempty `method`, `status`, `uri`, and
`exception` labels. Status is exactly three ASCII digits from 100 through 599.
Count/sum matching uses the entire source label identity; extra labels take part
in matching but are not stored. Matching state is bounded to 20,000 combined
identities. Invalid or duplicate series, mismatches, and overflowing aggregates
omit affected concepts and record focused warnings. Independent valid concepts
remain usable. Missing optional families do not warn. Compatibility requires a
valid CPU, heap, or process-start concept; uptime or HTTP metrics alone are
insufficient. Runtime-only idle exposition is compatible.

Memory is JVM heap used, not process RSS, container memory, or a configured
maximum heap size.

## HTTP metric behavior

HTTP timers are absent before the first completed request at startup and
restart, so HTTP samples remain unavailable until meters exist. Once present,
counters include application and management requests, including scrapes,
health requests, and inspection probes. Scrape timing differs: the tested 4.9.9
setup excludes its own unfinished request, while 5.x can record the scrape timer
before generating the response body. The first scrape can therefore already
contain real HTTP samples. StatLite stores raw cumulative counters and derives
nonnegative deltas at query time; process-start changes retain the existing
restart boundary semantics. Routes, exceptions, arbitrary labels, histogram
buckets, and percentiles are not stored.

## Health, JDBC health, and custom endpoints

Health is an independent optional request after the metrics attempt. For a
literal `/prometheus` suffix with at most one trailing slash, StatLite derives
same-origin `/health`, preserves the escaped context prefix, and removes the
query. A custom metrics path requires an explicit override for health:

```yaml
targets:
  - name: "orders"
    type: "micronaut"
    url: "http://localhost:8080/manage/metrics"
    health_url: "http://localhost:8080/manage/health"
```

`health_url` retains its exact path/query and may specify a different origin.
The target's Basic Auth applies to both endpoints. Redirects stay within each
endpoint's origin, and requests share the configured poll timeout and existing
body limits. When a health endpoint is configured or derived, StatLite attempts
at most one logical health request per poll unless derived health is cached
absent.

Application health requires a valid aggregate UP/DOWN `status` over HTTP 200 or
503. Unknown status, malformed payloads, or fetch errors leave health unavailable
and warn without discarding good metrics. Database health requires visible
`details.jdbc.status` with UP/DOWN. Absent or hidden details leave DB health
unavailable; invalid optional JDBC details warn while preserving valid app
health. Nested datasource details are ignored, and overall app health never
fills in DB health. The primary 5.x JDBC setup uses `micronaut-jdbc-hikari` 7.2.0
and H2 2.5.250; the retained 4.9.9 regression uses 6.2.1 and H2 2.3.232.
Exposing JDBC details to anonymous clients requires:

```properties
endpoints.health.details-visible=ANONYMOUS
```

The default authenticated visibility hides those details from anonymous
requests. Health absence never fabricates stored application or DB status.
Successful collection with unavailable explicit health can still display
operational `UP` on the dashboard, with its existing collection-based hint.

## Health caching and retries

An initial derived-health 404 with compatible metrics is quiet and cached until
a detectable process-start change or collector recreation. There is no periodic
reprobe. Enabling health without a detectable restart requires collector
recreation to discover it. After health was available, its loss warns and is
retried each poll. Explicit overrides always probe and warn on 404. These rules
preserve usable metrics and avoid carrying forward stale health values.

## Exact collection URLs and configuration limits

Collection requests the exact `url`, including its path, trailing slash, and
query, without endpoint discovery. Omitted `type` still defaults to Spring.
Spring-only `actuator_base_url`, `metrics_source`, and `collect_host_metrics`
are rejected for Micronaut, including explicit empty/false values. Host metrics
are not inferred from application metrics.

## Target inspection

```bash
statlite inspect --type micronaut 'http://localhost:8080'
statlite inspect --type micronaut 'http://localhost:8080/prometheus'
statlite inspect --type micronaut 'http://localhost:8080/service'
```

A root base URL resolves to `/prometheus`. A non-root path is tried exactly;
after HTTP 404/410 or parsed incompatible metrics, one context-path fallback
appends `/prometheus`. Conventional `/prometheus` paths and URLs with a query
(including a bare `?`) are exact and have no fallback. Escaped paths and queries
are preserved. Authentication failures, malformed responses, timeouts, and other
inconclusive failures stop resolution. Inspection has a five-second overall
deadline and at most two scrapes, each with at most three same-origin redirects.
It uses the runtime metrics evaluator, reports `partial` for compatible scrapes
with warnings, and does not request health. Compatibility does not prove
Micronaut identity. Untyped inspection adds no Micronaut probe or inference from
arbitrary Micrometer exposition. Inspection has no new authentication options;
configure authenticated targets manually with the shared Basic Auth block.

See [target inspection](../configuration.md#discover-a-target-with-inspect) for
shared options and configuration write modes, and
[Public integration testing](../integration-testing.md) for the demo checks.
