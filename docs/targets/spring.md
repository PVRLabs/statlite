# Spring Boot target

Spring Boot is StatLite's primary framework integration. Configure its Actuator
management base URL with `type: "spring"`.

See [Configuration](../configuration.md) for shared server, storage, polling,
and [Basic Auth](../configuration.md#basic-auth) settings. Add the target entry
to the `targets` list in your configuration.

## Basic configuration

```yaml
targets:
  - name: "my-app"
    type: "spring"
    url: "https://example.com/actuator"
    metrics_source: "auto"
    auth:
      type: "basic"
      username: "admin"
      password: "change-me"
```

## Management base URL

`url` is the Spring management base URL. StatLite derives the health and
metrics endpoints from it. `type` may be omitted for compatibility with older
Spring configurations, but new configuration should use explicit
`type: "spring"`.

## Metrics source and health

`metrics_source` accepts `auto`, `prometheus`, or `actuator` and defaults to
`auto` when omitted. Use `prometheus` to select the Spring Micrometer
Prometheus/OpenMetrics source explicitly, or `actuator` to select Actuator JSON.

`auto` prefers a compatible Spring Prometheus endpoint and
falls back to Actuator metrics only when the endpoint is absent or returns a
valid but incompatible exposition. Authentication, transient, malformed, and
resource-limit failures are retried without changing sources. Once selected,
the source remains fixed until the target collector is recreated. Health is
collected independently from the Actuator health endpoint. If health retrieval
fails but at least one independently usable metric sample is collected, the
poll remains successful, health stays unavailable, and StatLite records a
focused warning. If no usable metric sample is collected, the poll fails even
when health responded.

Missing optional metrics are handled gracefully: values may appear as `null` or charts may show gaps instead of failing the whole poll.

## Remote host metrics

Host metrics are disabled for Spring targets by default. In the common
single-host setup, monitor host CPU, memory, and disk through the
`statlite-self` target instead. For a remote Spring Boot application where
running StatLite on that host is undesirable, enable Actuator host collection
for that target:

```yaml
targets:
  - name: "remote-app"
    type: "spring"
    url: "https://remote.example.com/actuator"
    collect_host_metrics: true
```

This adds polls for `system.cpu.usage`, `disk.free`, and `disk.total`. The
resulting CPU and disk values describe the execution environment visible to
the Spring Boot process, which may be a container rather than the physical
host.

## Authentication and deprecated configuration

Spring endpoints use the target's `auth.type: "basic"` configuration. See
[Basic Auth](../configuration.md#basic-auth) for environment-variable credentials
and shared authentication settings.

`actuator_base_url` is deprecated; use `url`. See
[Deprecations and compatibility](../deprecations.md#actuator_base_url) for its
temporary compatibility behavior, including legacy embedded credentials.

## Examples and references

Run the [Spring Boot demo](../../examples/spring-actuator-demo/) for a standalone
application, traffic recipe, and restart test. The
[Actuator example configuration](../../examples/actuator.yaml) includes Basic
Auth placeholders. See [Supported integrations](../integrations.md#spring-boot)
for supported Spring versions and [Public integration testing](../integration-testing.md)
for the shipped fixture checks.

Spring memory is JVM heap used, not process RSS, container memory, or the
configured maximum heap size.
