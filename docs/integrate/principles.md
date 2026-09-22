# Lightweight integration principles

These principles guide StatLite's application-owned integrations, including
Django, FastAPI, Express, Go `net/http`, Gin, and similar future integrations.
They define implementation and certification boundaries. The
[StatLite Metrics v1 specification](../statlite-metrics-v1.md) remains the
authority for the wire format and field semantics.

## Report only what is correct

Metrics produced by an integration and derived by StatLite must be
semantically correct for the supported path. Never invent a response status,
health signal, restart, or metric value merely to populate a field. When an
integration cannot reliably observe something, omission is better than an
inaccurate value.

In particular:

- Database or dependency health requires an authoritative, inexpensive or
  cached application signal. Successful HTTP traffic is not such a signal.
- A response should not be classified when the framework or runtime no longer
  exposes its status reliably.
- Process-local counters must not be presented as application-wide counters.
- A convenient measurement must not replace a semantically different one,
  such as process RSS in place of runtime-managed heap.

## Optimize for the normal application path

Each integration should use the common, framework-native application path and
provide a small implementation that developers can understand. Certification
should thoroughly cover ordinary request and response counts, relevant HTTP
errors, cumulative duration, application status, process-start identity when
available, concurrency, metrics-endpoint exclusion, and framework behavior
that materially affects those signals.

The goal is not to emulate every extension, middleware ordering, custom
server, protocol upgrade, optional writer interface, deployment topology, or
unusual lifecycle. Do not turn every discovered edge case into a new
certification requirement.

## Optimize canonical helpers for the simple deployment first

Canonical helpers should provide useful application metrics with minimal code
and dependencies for one process or worker. Do not add complexity for
multi-process managers, replica aggregation, worker discovery, PID tracking,
fork detection, or framework-specific lifecycle edge cases unless there is
demonstrated demand.

Document those boundaries clearly. Multi-worker, prefork, and replica
deployments are outside the supported model of the simple copyable helpers.
Users who need those deployment models should open an issue or discussion so a
focused integration can be evaluated from concrete requirements.

## Keep application-owned code understandable

Copyable helpers and middleware are intentionally application code. Prefer
standard framework APIs, small explicit state, straightforward concurrency
protection, obvious registration, and a few well-explained behaviors.

Avoid dependencies, reflection, generalized instrumentation frameworks,
compatibility layers, or defensive machinery added only for rare cases. A
small helper that measures the normal path correctly and states its boundaries
is preferable to a larger helper that attempts universal transparency.

Use framework-native mechanisms when they improve correctness or simplicity.
For example, standard Go applications can use a bounded `net/http` wrapper,
while Gin should use Gin middleware rather than stretching the generic
wrapper around Gin. Django, FastAPI, and Express should likewise use their
established middleware mechanisms.

## Preserve behavior at the boundary

Unsupported or uncertified behavior should not unexpectedly break the
application where preserving it is practical. When an edge case cannot be
measured correctly:

1. Preserve application behavior.
2. Do not fabricate metrics.
3. Omit the affected observation or stop classifying it when practical.
4. Document the boundary when it is meaningful to users.

For example, after a Go HTTP connection is hijacked, raw protocol data may no
longer expose reliable HTTP completion, status, or duration semantics. The
helper preserves supported hijacking behavior but excludes that request from
its HTTP metrics. WebSockets and raw upgraded connections remain outside that
integration's certified metrics path.

## State the certified boundary

Integration documentation should distinguish among:

- behavior explicitly supported and tested;
- behavior preserved where practical but outside metrics certification; and
- behavior intentionally unsupported.

Exact compatibility with every optional API or framework configuration is not
required. If deeper support later becomes valuable, prefer the framework's
first-party or established integration contract instead of continually
expanding a generic helper.

## Preserve the v1 and deployment contracts

Application-owned integrations produce the existing
`statlite-metrics/v1` profile through `type: statlite-metrics`. They are not
new target types and must not introduce framework-specific metric
vocabularies. HTTP traffic, error, and duration signals use the fixed v1
fields, and optional fields remain optional. Application status belongs to the
application.

In-memory helpers may support concurrent traffic within one process, but
their counters remain process-local. They do not aggregate workers, processes,
containers, or replicas. Multi-process, prefork, and replica deployments are
outside the supported model of these simple helpers.

Simplicity and comprehensibility are product constraints. Do not solve
speculative compatibility problems unless they materially affect real users.
When substantially more complexity competes with leaving a safe niche behavior
outside the certified boundary, prefer the simpler integration.
