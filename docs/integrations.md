# Supported integrations

StatLite supports a small set of framework-owned application integrations.
Prometheus and OpenMetrics are wire formats used by some adapters; StatLite is
not a generic Prometheus scraper or metrics database.

## Support matrix

| Integration | Status | Metrics source(s) | Endpoint or configuration | Notes |
| --- | --- | --- | --- | --- |
| Spring Boot 3.5.x | Supported | Actuator JSON; Micrometer Prometheus/OpenMetrics | Spring Boot Actuator | Both sources are collected through the `spring` target. |
| Spring Boot 4.0.x | Supported | Actuator JSON; Micrometer Prometheus/OpenMetrics | Spring Boot Actuator | Both sources are collected through the `spring` target. |
| Spring Boot Actuator | Supported | Actuator JSON | `/actuator` management base URL | Health, application request, JVM, process, and optional host concepts are normalized into StatLite's fixed vocabulary. |
| Spring Micrometer Prometheus | Supported | Prometheus/OpenMetrics exposition | Configured Prometheus endpoint | This is a Spring source option, not a generic `prometheus` target. |
| Quarkus 3.39.x | In development | Micrometer Prometheus/OpenMetrics exposition | `/q/metrics` | Collection is being added as the explicit `quarkus` target. The pinned fixture uses Java 21 LTS. |
| StatLite Metrics v1 | Supported | Fixed `statlite-metrics/v1` response | `/statlite/metrics` | Fixed producer contract for StatLite and compatible applications. |

Support means that the integration has an owned endpoint and source contract,
bounded collection behavior, normalization rules, and regression or
certification evidence. A framework can appear here as “In development” without
being accepted as a production target yet. Supporting a source format means only
that the named adapter understands that source's documented contract; it does
not enable arbitrary metric ingestion.

## Spring Boot

Configure Spring applications with `type: spring` or omit `type` for the
default. The `url` is the Actuator management base URL. Spring can use its
Actuator source, its Micrometer Prometheus source, or the configured automatic
source selection described in [configuration](configuration.md).

## Quarkus

Quarkus support is being implemented as a framework-first `type: quarkus`
target. Its `url` will be the exact metrics exposition endpoint, conventionally
`http://localhost:9000/q/metrics`, rather than a management base URL. The
adapter will accept only the documented, bounded Quarkus/Micrometer contract;
it will not persist arbitrary source dimensions or infer health or host metrics
from a successful scrape.

The public pinned fixture is
[`examples/quarkus-metrics-demo/`](../examples/quarkus-metrics-demo/). It uses
Quarkus 3.39.1, Java 21 LTS, and the Micrometer Prometheus registry.

## Scope boundaries

StatLite does not currently provide a generic Prometheus target, arbitrary
metric storage, Prometheus querying, remote write, or a Prometheus-compatible
time-series database. Supported integrations expose only the normalized
concepts that StatLite can use for its dashboard, health, and diagnostics.
