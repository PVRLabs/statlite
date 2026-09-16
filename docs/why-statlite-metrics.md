# Why StatLite Metrics?

StatLite Metrics is an intentionally lightweight application integration for
small services and constrained or self-hosted environments. Choose it when the
fixed operational view that StatLite provides is expected to remain sufficient:
traffic, HTTP errors, average latency, application and optional database status,
restart and runtime signals, and basic process or host resources.

The [`statlite-metrics/v1`](statlite-metrics-v1.md) profile has a small,
language-neutral vocabulary. It has no arbitrary labels, application-defined
metric families, or producer-defined metric names. An application exposes only
the operational concepts that StatLite consumes in a bounded JSON response.
StatLite retrieves one inexpensive snapshot with one `GET` request per poll.

This bounded design keeps the producer and its ongoing maintenance
straightforward. It is a good fit when an application does not already have a
framework integration that StatLite supports and does not need a broader
instrumentation system solely for this operational view. See the
[integration guide index](integrate/) to choose between a first-class
integration and direct v1 integration.

## Health and reporting

The required `status` value is the producer's application-level operational
assertion. An application may also provide `database_status` when it already
has an authoritative, inexpensive or cached database-health signal. It should
not run a database query just to answer a metrics poll or invent a dependency
status when no suitable signal exists.

A successful response shows that the metrics endpoint is available and
reporting. It does not prove that every part of the application or every
dependency is healthy. The profile keeps application status, optional database
status, and reporting availability distinct.

## Relationship to broader instrumentation

Prometheus, OpenTelemetry, and similar systems address broader instrumentation
needs. A Prometheus scrape is normally one request, just as a StatLite Metrics
snapshot is. Depending on the framework, metrics and authoritative application
or dependency health may still be separate contracts or endpoints.

StatLite Metrics makes a different tradeoff: its payload and vocabulary are
fixed around the concepts StatLite stores and displays. It is not a replacement
for arbitrary metrics, labels, traces, logs, percentiles, or exploratory
queries. Applications may use broader instrumentation alongside StatLite
Metrics or add it later if their needs grow.

The [StatLite Metrics v1 specification](statlite-metrics-v1.md) is the
authoritative endpoint and field contract.
