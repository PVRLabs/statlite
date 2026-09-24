# Integrate an application with StatLite

Use this page to choose an application integration and find a copyable guide.
These guides are for small and self-hosted applications that need traffic,
errors, latency, application status, and basic process signals without
operating a Prometheus server and Grafana stack.

## Choose an integration path

1. Check the [supported integrations](../integrations.md). If StatLite has a
   first-class integration for your framework, use that target type and its
   documented framework contract.
2. If StatLite does not have a first-class integration, check the framework
   guides below. Use the direct
   [`statlite-metrics/v1`](../statlite-metrics-v1.md) integration when the
   framework's execution model fits the lightweight application-owned
   endpoint. Your application exposes `GET /statlite/metrics`, and StatLite
   polls that bounded JSON endpoint with `type: statlite-metrics`.
3. Follow the framework guide below to add the application middleware, helper,
   and endpoint. Generating StatLite YAML does not replace the required
   application integration.

Both paths are intentional choices. First-class integrations are useful when a
framework has a stable, recognizable contract and enough value or demand to
maintain it. Direct v1 integration is a small, language-neutral choice for
applications whose operational needs fit StatLite's fixed vocabulary. Read
[Why StatLite Metrics?](../monitoring-options.md#why-statlite-metrics) for the product and
technical tradeoffs. The [lightweight integration principles](principles.md)
record the implementation philosophy and certification boundaries shared by
application-owned integrations.

## Framework guides

| Framework | Guide | Runnable example |
| --- | --- | --- |
| FastAPI | [FastAPI](python/fastapi.md) | [FastAPI demo](../../examples/python-fastapi-demo/) |
| Express | [Express](node/express.md) | [Express demo](../../examples/node-express-demo/) |
| Django | [Django](python/django.md) | [Django demo](../../examples/python-django-demo/) |
| Go `net/http` | [Go `net/http`](go/net-http.md) | [Go net/http demo](../../examples/go-net-http-demo/) |
| Gin | [Gin](go/gin.md) | [Go Gin demo](../../examples/go-gin-demo/) |

These frameworks do not have first-class target types; their guides use
`type: statlite-metrics`. The stable guide locations are suitable for linking
from configuration tools. Each guide uses the conventional `/statlite/metrics`
endpoint and requires an application setup step.

## Memory reported by each integration

The dashboard presents one normalized application-memory series. Each runtime
provides the closest useful, low-overhead value through
`runtime_heap_used_bytes`:

| Integration | Memory value |
| --- | --- |
| FastAPI and Django | Current Python allocations traced by `tracemalloc` |
| Express | Current V8 heap used from `process.memoryUsage().heapUsed` |
| Go `net/http` and Gin | Current allocated Go heap from `runtime.MemStats.Alloc` |

These values are runtime-managed application memory. They are not process RSS,
container memory, memory limits, or maximum heap values. First-class Spring
Boot and Quarkus integrations report JVM heap used. StatLite self-monitoring
reports current allocated Go heap with the same meaning as the Go helpers.

Application integrations should focus on application and runtime signals they
can measure reliably. On a collocated VPS, the StatLite self target supplies
basic CPU, memory, and disk trends for the environment visible to StatLite.
Framework helpers do not need to collect host metrics. See the
[integration principles](principles.md)
and [deployment topology](../product.md#deployment-topology) for the remote
host boundary.

## Common guide structure

Framework guides use this sequence:

1. When to use the integration and whether a first-class target exists.
2. The minimal dependency-light integration.
3. An established-library path only when it is materially useful, or an
   explicit statement that no additional library path is recommended.
4. Complete middleware, helper, endpoint, and exact `GET /statlite/metrics`
   behavior.
5. Required and optional fields, including status semantics.
6. StatLite YAML.
7. Verification with `curl`, `statlite inspect`, and StatLite.
8. Optional metrics and deployment caveats.
9. Canonical references and criteria for possible future first-class support.

The [v1 specification](../statlite-metrics-v1.md) remains authoritative for
the wire contract. See [configuration](../configuration.md) for all StatLite
options and [target inspection](../configuration.md#discover-a-target-with-inspect)
for the bounded, read-only discovery workflow.

## Interested in a first-class integration?

StatLite keeps integrations intentionally small and bounded. When a framework
or platform has a stable, recognizable monitoring contract that maps well to
StatLite's focused metrics, a first-class integration may make sense.

If you maintain or use a framework, library, or application with a monitoring
interface that could support a reliable StatLite integration, [open an issue](https://github.com/PVRLabs/statlite/issues/new/choose)
or [start a GitHub Discussion](https://github.com/PVRLabs/statlite/discussions/new/choose).
We are happy to evaluate the contract and work with maintainers and users on a
focused integration.
