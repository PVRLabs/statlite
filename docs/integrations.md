# Supported integrations

StatLite supports a small set of framework/application integrations, including
explicitly certified setups where no framework-owned contract exists.
Prometheus and OpenMetrics are wire formats used by some adapters; StatLite is
not a generic Prometheus scraper or metrics database.

## Support matrix

| Integration | Status | Metrics source(s) | Endpoint or configuration | Notes |
| --- | --- | --- | --- | --- |
| Spring Boot 3.5.x | Supported | Actuator JSON; Micrometer Prometheus/OpenMetrics | Spring Boot Actuator | Both sources are collected through the `spring` target. |
| Spring Boot 4.0.x | Supported | Actuator JSON; Micrometer Prometheus/OpenMetrics | Spring Boot Actuator | Both sources are collected through the `spring` target. |
| Spring Boot Actuator | Supported | Actuator JSON | `/actuator` management base URL | Actuator health is the normal Spring health source; application request, JVM, process, and optional host concepts are normalized into StatLite's fixed vocabulary. |
| Spring Micrometer Prometheus | Supported | Prometheus/OpenMetrics exposition | Configured Prometheus endpoint | This is a Spring source option, not a generic `prometheus` target. |
| Quarkus 3.39.x | Supported | Micrometer Prometheus/OpenMetrics exposition; optional SmallRye Health | Conventional `/q/metrics`; optional `/q/health` | Explicit `quarkus` target; datasource health is normalized when published. |
| Micronaut 4.9.9 (certified setup) | Supported | Micrometer 1.15.0 Prometheus exposition; optional management health | Exact `/prometheus`; optional `/health` | Explicit `micronaut` target; JDBC health requires visible aggregate details. |
| StatLite Metrics v1 | Supported | Fixed `statlite-metrics/v1` response | `/statlite/metrics` | Fixed producer contract for StatLite and compatible applications. See the [direct integration guides](integrate/). |

Support means that the integration has an owned endpoint and source contract,
bounded collection behavior, normalization rules, and regression or
certification evidence that can be verified through recognized signals,
inspection, fixtures, or tests. A framework can appear here as “In development”
without being accepted as a production target yet. Where an ecosystem has no
stable framework-owned contract, support may be limited to one explicitly
documented and tested library/configuration/metric setup. That certification
covers only the named setup and contract, not arbitrary metrics emitted by the
language or framework. Supporting a source format means only that the named
adapter understands that source's documented contract; it does not enable
arbitrary metric ingestion.

See [Public integration testing](integration-testing.md) for how the shipped
examples are exercised in the shared public workflow.

## Spring Boot

Configure Spring applications with `type: spring` or omit `type` for the
default. The `url` is the Actuator management base URL. Spring can use its
Actuator source, its Micrometer Prometheus source, or the configured automatic
source selection described in [configuration](configuration.md). Health is an
independent authoritative Actuator signal. If its retrieval fails, StatLite
leaves health unavailable, retains independently usable metrics, and records a
focused warning. A poll still requires at least one usable metric sample to
count as reporting.

## Quarkus

Quarkus is a framework-first `type: quarkus` target. Its `url` is the exact
metrics exposition endpoint, conventionally
`http://localhost:9000/q/metrics`, rather than a management base URL. The
adapter accepts only the documented, bounded Quarkus/Micrometer contract; it
does not persist arbitrary source dimensions or infer host metrics from a
successful scrape. SmallRye Health is an optional Quarkus capability. For a
conventional Quarkus metrics path ending in `/q/metrics`, StatLite derives the
sibling `/q/health` endpoint where practical, requests it separately when
available, and normalizes the overall and datasource statuses. A missing health
capability is quiet: aggregate framework health is unavailable, while a
successful metrics scrape is presented as `UP` on the dashboard. Its hint
explains that `UP` is derived from successful metrics collection rather than
explicit application health. A current collection failure is presented as
`DOWN`. These are presentation labels only: reporting and unavailable remain
the underlying collection concepts, and StatLite does not synthesize stored
health values.
Database health remains unavailable unless a datasource check is published.
The absent capability is cached until the observed process-start identity
changes when available, or the collector is recreated. A known health failure
can record a focused warning without discarding valid metrics. For a customized
layout, set `health_url` as an optional override; a custom metrics path without
that override remains a supported metrics-only target.

The normalized concepts are HTTP request count and duration, 404/4xx/5xx
counts, process CPU, heap used, process start time, and optional uptime. HTTP
meters are lazy, so an idle endpoint can be compatible with finite runtime
families alone. Typed inspection accepts either an application base URL or an
exact customized metrics endpoint. Untyped inspection probes only the
established `/q/metrics` location; it does not identify arbitrary Micrometer
exposition as Quarkus. Basic Auth uses the shared `auth.type: basic`
configuration for both endpoints.

Spring Boot, Quarkus, and Micronaut memory is JVM heap used. It is runtime-managed
application memory, not process RSS, container memory, or a configured maximum
heap size.

The public pinned fixture is
[`examples/quarkus-metrics-demo/`](../examples/quarkus-metrics-demo/). It uses
Quarkus 3.39.1, Java 21 LTS, the Micrometer Prometheus registry, and SmallRye
Health.

## Micronaut

Micronaut uses an explicit `type: micronaut` target and the exact metrics
endpoint, conventionally `http://localhost:8080/prometheus`. The certified
setup uses Micronaut 4.9.9 (platform parent 4.9.2), Micronaut Micrometer 5.12.0,
Micrometer 1.15.0, and optional JDBC Hikari integration 6.2.1. Certification
uses Temurin 25.0.4.1+1-LTS with Java 17 fixture bytecode and H2 2.3.232.
Support covers this setup and the fixed contract, not arbitrary Micrometer
exposition. Quarkus and Micronaut retain separate label and health contracts.

The adapter normalizes request count, 404/4xx/5xx counts, accumulated duration,
process CPU, JVM heap used, process start, and uptime. HTTP timers are lazy at
idle and restart, and include management self-traffic once requests complete.
Missing timers remain unavailable. Source routes, exceptions, datasource names,
and other labels are not stored as dimensions. Invalid concepts produce focused
partial warnings while independent valid metrics remain usable.

Management health is independent and optional. A conventional `/prometheus`
path derives sibling `/health`; custom paths can use `health_url`. Database
health requires visible `details.jdbc.status`, and remains unavailable when
JDBC details are absent or hidden. UP/DOWN aggregate application health is
accepted over HTTP 200 or 503. Metrics success does not synthesize stored health.
As with Quarkus, successful metrics without explicit health can appear as
operational `UP` on the dashboard. Initial derived-health absence is cached until
a detectable process restart or collector recreation; known failures warn and
are retried. Disabling health can leave metrics usable; removing management
from the certified dependency graph removes both endpoints.

Use `statlite inspect --type micronaut` with a base URL or exact endpoint.
Inspection checks the same metrics contract as collection and does not probe
health. It does not prove Micronaut identity, and untyped inspection does not
attempt Micronaut recognition. See [configuration and setup](configuration.md#micronaut-micrometer-metrics)
for dependencies, endpoint resolution, health visibility, and overrides.

## Scope boundaries

For applications without a first-class framework target, use the [StatLite
Metrics integration path](integrate/) when the application can own the fixed
`/statlite/metrics` endpoint and its cumulative state. The guides cover
FastAPI, Express, Django, Go `net/http`, and Gin; application integration is
still required.

StatLite does not currently provide a generic Prometheus target, arbitrary
metric storage, Prometheus querying, remote write, or a Prometheus-compatible
time-series database. Supported integrations expose only the normalized
concepts that StatLite can use for its dashboard, health, and diagnostics.

Future first-class Caddy, Go, Gin, Node, Python, and other framework adapters
must omit
health when their certified integration contract has no explicit authoritative
health signal. Metrics collection alone establishes reporting availability.
