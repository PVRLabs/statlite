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
| [Micronaut 5.2.11 (tested setup)](targets/micronaut.md#tested-versions) | Supported | Micrometer 1.17.1 Prometheus exposition; optional management health | Exact `/prometheus`; optional `/health` | Primary tested setup; explicit `micronaut` target; JDBC health requires visible aggregate details. |
| [Micronaut 4.9.9 (tested setup)](targets/micronaut.md#tested-versions) | Supported | Micrometer 1.15.0 Prometheus exposition; optional management health | Exact `/prometheus`; optional `/health` | Retained regression setup used by the public demo and CI; JDBC health requires visible aggregate details. |
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
Actuator source, its Micrometer Prometheus source, or
[automatic source selection](targets/spring.md#metrics-source-and-health). Health is an
independent authoritative Actuator signal. If its retrieval fails, StatLite
leaves health unavailable, retains independently usable metrics, and records a
focused warning. A poll still requires at least one usable metric sample to
count as reporting.

See the [Spring target reference](targets/spring.md) for configuration,
source selection, health, and remote host metrics.

## Quarkus

Quarkus is a first-class `type: quarkus` target with an exact metrics endpoint,
normally `/q/metrics`, and optional SmallRye Health. Support covers StatLite's
bounded Quarkus/Micrometer contract, including datasource health when published.
See the [Quarkus target reference](targets/quarkus.md) for normalized concepts,
compatibility, health behavior, custom endpoints, inspection, and the pinned
Quarkus 3.39.1 / Java 21 fixture.

## Micronaut

Micronaut is a first-class explicit `type: micronaut` target with an exact
metrics endpoint, normally `/prometheus`, and optional management health.
Support covers the named 5.2.11 and 4.9.9 setups and fixed contract, not arbitrary
Micrometer exposition. Quarkus and Micronaut retain separate label and health
contracts. See the [Micronaut target reference](targets/micronaut.md) for
application setup, tested versions, normalization, JDBC health visibility,
custom endpoints, and typed inspection.

Spring Boot, Quarkus, and Micronaut memory is JVM heap used. It is runtime-managed
application memory, not process RSS, container memory, or a configured maximum
heap size.

## Scope boundaries

For applications without a first-class framework target, use the [StatLite
Metrics integration path](integrate/) when the application can own the fixed
`/statlite/metrics` endpoint and its cumulative state. The guides cover
FastAPI, Express, Django, Go `net/http`, and Gin.
The [Javalin recipe](integrate/java/javalin.md) is outside integration CI.
Application integration is still required.

StatLite does not currently provide a generic Prometheus target, arbitrary
metric storage, Prometheus querying, remote write, or a Prometheus-compatible
time-series database. Supported integrations expose only the normalized
concepts that StatLite can use for its dashboard, health, and diagnostics.

Future first-class Caddy, Go, Gin, Node, Python, and other framework adapters
must omit
health when their certified integration contract has no explicit authoritative
health signal. Metrics collection alone establishes reporting availability.
