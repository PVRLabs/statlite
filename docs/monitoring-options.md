# Monitoring options for small applications and VPS deployments

Monitoring tools optimize for different environments. A single application on
a 512 MiB or 1 GiB VPS has different constraints from a distributed system with
many services, custom telemetry, logs, traces, and dedicated infrastructure.

StatLite is designed for the smaller case. It combines collection for supported
applications, local SQLite history, retention, host visibility, and a dashboard
in one small process. Current measurements show roughly 10 to 15 MiB of idle
RSS. This is an observed range, not a maximum-memory guarantee.

Beyond its Spring Boot and Quarkus integrations, applications can expose the
small, fixed [StatLite Metrics v1 profile](statlite-metrics-v1.md) to use the
same collection, history, and dashboard without adopting a general-purpose
telemetry pipeline.

> **In short:** StatLite trades observability breadth for a very small complete
> deployment, local control, and low operational complexity.

## Is StatLite a good fit?

StatLite is a good fit when:

- an application runs on a small VPS or server where memory still matters;
- focused health, traffic, error, latency, CPU, memory, and disk signals answer
  the important operational questions;
- one monitoring process is preferable to assembling a monitoring stack;
- SQLite is sufficient for local history;
- telemetry should be able to remain on the monitored infrastructure; and
- a 90-day default with locally configurable retention is useful.

Use a broader monitoring or observability platform when you need distributed
tracing, centralized logs, arbitrary custom metrics and queries, deep
application performance monitoring, a large integration ecosystem, or
fleet-scale operation.

## Three ways to deploy monitoring

The most useful comparison is not a generic feature scorecard. It is what must
run locally, where monitoring data is stored, and what the operator must manage.

| Question | StatLite | General-purpose self-hosted stack | Agent or instrumentation with another backend |
| --- | --- | --- | --- |
| What runs locally? | One StatLite process | Metrics server, dashboard, and exporters or instrumentation as needed | Agent, SDK, Collector, or some combination |
| Where are storage and dashboards? | On the monitored infrastructure | On infrastructure selected by the operator | In a selected self-hosted or hosted backend |
| Who controls retention? | The operator, through local configuration and disk capacity | The operator or the selected backend | The selected backend or service plan |
| Local operational work | One deliberately focused component | Multiple flexible components | Local collection plus any backend the operator manages |
| Typical strength | Small, complete, local monitoring | Flexible metrics, querying, and dashboards | Portable telemetry pipelines or broad managed observability |
| Common examples | StatLite | Prometheus and Grafana | OpenTelemetry, Datadog, and New Relic |

This distinction matters when comparing memory figures. StatLite's roughly 10
to 15 MiB idle RSS represents its complete process, including collection,
SQLite storage, queries, and the web dashboard. A hosted platform's local-agent
figure covers collection and forwarding, while storage, querying, and
dashboards run elsewhere.

## Can the complete system share a small VPS?

StatLite is explicitly designed to coexist with an application on a
resource-constrained server. A controlled 512 MiB VPS experiment ran StatLite
beside a representative Spring Boot application for 60 minutes without a
restart. StatLite finished at approximately 12 MiB RSS. The result is evidence
for that tested workload, not a promise that every application will fit the
same machine. See [Monitoring on resource-constrained
servers](low-resource-monitoring.md) for the method, results, and limitations.

The architectural alternatives make different tradeoffs:

- **StatLite:** a small complete system, with collection, storage, retention,
  and visualization kept local.
- **Prometheus and Grafana:** a complete and much more flexible local system,
  assembled from multiple components.
- **Hosted platforms:** a local collection component with most of the system
  operated remotely by the provider.
- **OpenTelemetry:** instrumentation and a telemetry pipeline whose complete
  footprint depends on the chosen backend.

## Why not Prometheus and Grafana?

Prometheus and Grafana are the closest general-purpose, self-hosted
alternative. They provide arbitrary metrics, PromQL, a large exporter
ecosystem, powerful dashboards, and far more customization than StatLite.
Prometheus includes a local on-disk time-series database and supports locally
configured time- or size-based retention. The [Prometheus storage
documentation](https://prometheus.io/docs/prometheus/latest/storage/) currently
documents a 15-day default when neither limit is configured.

That flexibility has a different resource and operational profile. Grafana's
[installation documentation](https://grafana.com/docs/grafana/latest/setup-grafana/installation/)
currently recommends at least 512 MB of memory and one CPU core. Grafana calls
that an evaluation floor and states that it covers the Grafana server process
only, excluding data sources such as Prometheus. Prometheus, exporters, and the
workload add their own usage; no single Prometheus memory figure applies to
every deployment.

StatLite combines a narrower monitoring model into one process that typically
uses roughly 10 to 15 MiB at idle. Its 90-day default retention is configurable
locally. It does not provide PromQL, arbitrary metrics, or Grafana's dashboard
flexibility.

**Decision rule:** choose Prometheus and Grafana when you need a general-purpose
metrics platform. Choose StatLite when its predefined application and host
signals are enough and keeping the complete monitoring system very small
matters.

## Why not OpenTelemetry?

OpenTelemetry solves a different layer of the problem. Its own [project
overview](https://opentelemetry.io/docs/what-is-opentelemetry/) describes it as
a framework and toolkit for generating, collecting, and exporting telemetry,
not as an observability backend. Storage and visualization are intentionally
left to other tools. The Collector can receive, process, and export telemetry
to one or more backends.

StatLite solves a smaller end-to-end problem. For supported applications it
provides collection, local history, retention, and visualization without
requiring the operator to select a telemetry backend.

Pairing a self-hosted OpenTelemetry Collector with a local storage and
visualization backend creates a deployment closer to the general-purpose
Prometheus and Grafana case: the complete footprint includes the Collector,
backend, and dashboard.

**Decision rule:** choose OpenTelemetry when vendor-neutral instrumentation,
distributed traces, telemetry pipelines, or backend portability matter. Choose
StatLite when straightforward local application and host monitoring is the
whole requirement.

## Why not a hosted observability platform?

Hosted platforms remove most of the work involved in operating a backend and
provide capabilities far beyond StatLite. Their normal architecture leaves an
agent or SDK on the server and sends telemetry to a provider-operated service.
That can be an excellent trade when broad observability and a managed backend
matter more than keeping the complete monitoring path local.

### New Relic

New Relic offers a broad platform and a free tier. Its [infrastructure agent
benchmark](https://docs.newrelic.com/docs/infrastructure/infrastructure-agent/manage-your-agent/agent-overhead/)
reports 25 to 35 MB of resident memory for its documented Linux single-task
host. That is a small local footprint, but it represents the agent only.
Storage, querying, and dashboards run in New Relic's hosted service. The
benchmark also uses an 8-vCPU, 32 GB EC2 host, so it should not be treated as a
guarantee for every small VPS.

New Relic currently advertises [default retention of at least eight
days](https://newrelic.com/pricing/free-tier) with its free tier. Actual
retention varies by data type. Its [retention
documentation](https://docs.newrelic.com/docs/data-apis/manage-data/manage-data-retention/)
currently lists eight days for categories including APM, APM errors,
infrastructure processes, and distributed traces, while other categories are
retained longer. Longer-retention data options are available.

StatLite defaults to 90 days. Its database is local, so the operator can change
retention according to available disk space rather than a hosted-service data
policy.

**Decision rule:** choose New Relic when a managed backend and broader
observability capabilities are worth sending telemetry to a hosted service.
Choose StatLite when focused monitoring is enough, metrics should remain local,
and locally controlled retention matters.

### Datadog

Datadog provides managed infrastructure monitoring, application monitoring,
integrations, dashboards, and querying. Its [Agent
documentation](https://docs.datadoghq.com/getting_started/agent/) states that
the Agent collects host events and metrics and sends them to Datadog. Its
[architecture documentation](https://docs.datadoghq.com/agent/architecture/)
describes the forwarder that sends payloads over HTTPS. Agent resource use
depends on the enabled features, so this page does not use a single memory
number for it.

StatLite takes the opposite architectural approach: collection, storage,
retention, and visualization can all remain on the monitored infrastructure.

**Decision rule:** choose Datadog when broad, managed observability is the
requirement. Choose StatLite when the requirement is much smaller and keeping
the complete monitoring path local and lightweight is more important.

## What actually runs where?

These are simplified architectures. Real deployments can contain additional
components.

```text
StatLite
application -> StatLite -> local SQLite and dashboard

Prometheus and Grafana
application or exporter -> Prometheus -> Grafana

OpenTelemetry
application or SDK -> Collector or exporter -> chosen backend -> dashboard

Hosted platform
application or host -> local agent -> provider-operated storage and dashboard
```

## What StatLite deliberately leaves out

StatLite stays small partly because it deliberately does less. It is not
intended to replace a complete observability platform. It does not aim to
provide:

- distributed tracing;
- centralized log management;
- deep exception diagnosis or full APM;
- arbitrary telemetry pipelines;
- unrestricted custom metrics and querying;
- highly customized dashboards; or
- a large integration ecosystem.

Those are not hidden limitations. They are capabilities that broader tools add
in exchange for additional infrastructure, a hosted service, or both. If those
capabilities become requirements, moving to a broader platform is the natural
next step.

## The StatLite tradeoff

StatLite occupies the lightweight, local end of monitoring. It gives up broad
observability features in exchange for a complete monitoring process that
typically uses roughly 10 to 15 MiB of idle RSS, keeps its SQLite history
locally, defaults to 90 days of retention, and leaves retention under operator
control.

If those constraints are advantages, StatLite is likely a good fit. If they are
not, a broader self-hosted or managed platform will provide more room to grow.

Competitor documentation and product terms change. The external facts on this
page were last checked against the linked first-party sources on 2026-09-17.
