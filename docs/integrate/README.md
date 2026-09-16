# Integrate an application with StatLite

Use this page to choose an application integration and find a copyable guide.

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
[Why StatLite Metrics?](../why-statlite-metrics.md) for the product and
technical tradeoffs.

## Framework guides

| Framework | First-class target type | Guide |
| --- | --- | --- |
| FastAPI | No | [FastAPI](python/fastapi.md) |
| Express | No | [Express](node/express.md) |
| Django | No | [Django](python/django.md) |

These stable guide locations are suitable for linking from configuration
tools. Each direct-integration guide uses `type: statlite-metrics`, the
conventional `/statlite/metrics` endpoint, and an explicit application setup
step.

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
