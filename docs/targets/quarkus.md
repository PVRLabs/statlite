# Quarkus target

Configure the exact Quarkus Micrometer metrics endpoint with `type: "quarkus"`.

See [Configuration](../configuration.md) for shared server, storage, polling,
and [Basic Auth](../configuration.md#basic-auth) settings. Add the target entry
to the `targets` list in your configuration.

## Basic configuration

```yaml
targets:
  - name: "orders"
    type: "quarkus"
    url: "http://localhost:9000/q/metrics"
```

## Endpoints and tested setup

For Quarkus, `url` is the conventional `/q/metrics` Prometheus/OpenMetrics
endpoint, not a management base URL. StatLite derives the aggregate SmallRye
Health endpoint by replacing `/q/metrics` with `/q/health` on the same origin
and context path when the conventional capability is available. It performs
one bounded health request and one bounded metrics scrape per polling cycle
when health is configured or conventionally available, and uses the poll time
rather than exposition timestamps. The pinned fixture includes Quarkus 3.39.1
with Java 21 LTS, `quarkus-micrometer-registry-prometheus`, and the optional
`quarkus-smallrye-health` extension.

## Health and custom endpoints

Health collection is best-effort and independent from metrics collection. If
the derived `/q/health` endpoint is absent, aggregate framework health is
unavailable. A successful metrics scrape is shown as `UP` on the dashboard,
with its hint explaining that the label is based on collection rather than an
explicit application-health assertion. Internally this remains reporting
availability; StatLite does not synthesize or store application health `UP`.
Database health remains unavailable without a datasource check. The absent
capability is quiet and does not produce a recurring warning. A known or
explicitly configured endpoint that returns an invalid or failed response may
produce a focused warning without discarding valid metrics. Exact custom
metrics paths remain supported; when the path is not a conventional
`/q/metrics` path, StatLite does not infer a health endpoint.
Customized Quarkus layouts can provide an exact optional override:

```yaml
targets:
  - name: "orders"
    type: "quarkus"
    url: "http://localhost:9000/manage/prom"
    health_url: "http://localhost:9000/manage/health"
```

`health_url` is optional and accepted for Quarkus and Micronaut targets. The target's
Basic Auth configuration applies to both metrics and health requests.

If the derived `/q/health` endpoint returns `404`, StatLite treats SmallRye
Health as absent, keeps the metrics poll quiet, and leaves application health
unavailable. A successful metrics scrape is still shown as `UP`, with the
dashboard hint identifying successful metrics collection as the source. A
current collection failure is shown as `DOWN`; the underlying collection
states remain reporting and unavailable. That absence is cached for the
collector session. Health discovery resumes when
the observed process-start identity changes, when that identity is available,
or when the collector is recreated.

## Normalized metrics and compatibility

The adapter normalizes only these existing StatLite concepts: HTTP request
count, request duration, 404/4xx/5xx counts, process CPU ratio, heap used bytes,
process start time, and optional uptime. Request dimensions, histogram buckets,
exemplars, timestamps, and unrelated metric families are discarded before
persistence. HTTP meters can be absent while an idle application remains
compatible when a finite CPU, heap, or process-start family is present.

When published, Quarkus targets normalize overall SmallRye Health status and
aggregate Quarkus datasource health checks into `db_health_status`. Database
health stays unavailable when the application publishes no datasource check.
Host resources are not inferred or populated. Missing optional concepts produce
partial data; an endpoint without a usable required runtime family is
incompatible.

Memory is JVM heap used, not process RSS, container memory, or a configured
maximum heap size. StatLite accepts the bounded Quarkus/Micrometer contract,
not arbitrary Prometheus exposition.

## Target inspection

```bash
statlite inspect --type quarkus 'http://localhost:9000'
statlite inspect --type quarkus 'http://localhost:9000/q/metrics'
```

Typed Quarkus inspection accepts only StatLite's bounded Quarkus contract, not
arbitrary Prometheus or Micrometer exposition. A root application URL resolves
to `/q/metrics`. A non-root URL is tried first as an exact endpoint and then,
after a conclusive miss, with `/q/metrics` appended. A URL containing a query
string is always an exact endpoint and is preserved.

Untyped inspection probes the established `/q/metrics` location; it does not
identify arbitrary Micrometer exposition as Quarkus. See
[target inspection](../configuration.md#discover-a-target-with-inspect) for
shared inspection options and configuration write modes.

## Example and testing

The [pinned Quarkus fixture](../../examples/quarkus-metrics-demo/) includes
contract captures and a traffic recipe. See
[Public integration testing](../integration-testing.md) for the shipped checks.
