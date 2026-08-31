# Configuration

StatLite supports Spring Boot Actuator and the canonical fixed StatLite Metrics
JSON profile for small applications that need basic health, traffic, latency,
CPU, and runtime memory monitoring without a full observability stack.
StatLite is not a Prometheus/Grafana replacement.

StatLite loads `statlite.yaml` by default. Override with `--config`:

```bash
./statlite --config examples/actuator.yaml
# or, with an installed binary:
statlite --config /etc/statlite/config.yaml
```

See `examples/` for starter templates (Actuator, StatLite Metrics, multi-target,
self-monitoring), `examples/python-fastapi-demo/` for a runnable FastAPI app,
and `examples/spring-actuator-demo/` for a standalone Spring Boot demo app.

## Discover a target with `inspect`

When you know an application's base URL but not its metrics endpoint, run:

```bash
statlite inspect 'http://localhost:8080'
```

Inspection probes the conventional `/actuator/health` and
`/statlite/metrics` paths. A recognized Spring application gets a conditional
`/actuator/metrics` capability check; if neither conventional integration is
recognized, the supplied base URL is tried once as a direct StatLite Metrics
fallback. The command prints a copyable minimal configuration and the next
command; for a new setup, save the printed configuration as `statlite.yaml`.
With an existing configuration, copy only the target entry into its `targets`
list before running StatLite.

> [!IMPORTANT]
> Quote browser-pasted URLs, especially URLs containing `?` or `&`. Inspection
> accepts only an absolute HTTP or HTTPS application URL without a query string,
> fragment, or user information. It is bounded, read-only, and does not load
> configuration, create SQLite state, or start monitoring. It does not support
> authentication flags. Authentication-required, unreachable, timed-out,
> malformed, unknown, or ambiguous responses produce an error without YAML.

## Server

```yaml
server:
  # Localhost by default; use 0.0.0.0 only behind firewall/VPN/proxy auth.
  listen: "127.0.0.1:9090"
```

StatLite has no built-in dashboard/API authentication. Keep `listen` on loopback unless access is protected externally. Listening on a non-loopback address is normal inside a container, but ensure the published port is restricted or protected by a firewall, VPN, SSH tunnel, or authenticated reverse proxy.

## Storage

```yaml
storage:
  sqlite_path: "./statlite.sqlite"
  # Default is 90 days when omitted; set to 0 for unlimited retention.
  retention_days: 90
```

`sqlite_path` must be writable by the StatLite process. Runtime SQLite files (`*.sqlite`, `*.sqlite-shm`, `*.sqlite-wal`) should not be committed.

### Retention

StatLite keeps SQLite history for **90 days** by default. On startup, and then every 24 hours while running, it deletes poll snapshots older than the configured retention window; related metric samples and collector events are removed automatically.

Set `retention_days: 0` to disable cleanup and keep history indefinitely. Existing SQLite files are pruned on the first startup after retention is enabled unless retention is set to `0`.

## Polling

```yaml
polling:
  interval: "30s"
  timeout: "10s"
```

* `interval`: how often each target is polled (Go duration, required).
* `timeout`: per-poll HTTP timeout (Go duration; default `10s` if omitted).

Use `30s` or longer for production deployments. Shorter intervals increase
HTTP requests, SQLite writes, and database growth, and are best reserved for
explicitly labeled local demos.

Each target is polled immediately on startup. If that first poll succeeds and
needs a new counter baseline because the target has no stored history or starts
a new application run, StatLite makes one follow-up poll after three seconds,
or after the configured interval when it is shorter. Gauge-only targets and
targets with a compatible stored baseline use the configured interval normally.

## Targets

At least one target is required. Names must be unique.

### Spring Boot Actuator

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

`url` is the Spring management base URL. StatLite derives the health and
metrics endpoints from it. `type` may be omitted for compatibility with older
Spring configurations, but new configuration should use explicit
`type: "spring"`.

`metrics_source` accepts `auto`, `prometheus`, or `actuator` and defaults to
`auto` when omitted. `auto` prefers a compatible Spring Prometheus endpoint and
falls back to Actuator metrics only when the endpoint is absent or returns a
valid but incompatible exposition. Authentication, transient, malformed, and
resource-limit failures are retried without changing sources. Once selected,
the source remains fixed until the target collector is recreated. Health is
always collected independently from the Actuator health endpoint.

The former `actuator_base_url` field remains a deprecated compatibility alias.
StatLite logs a warning and treats it as `url`. Configuring both fields is an
error, even when their values are identical.

Missing optional metrics are handled gracefully: values may appear as `null` or charts may show gaps instead of failing the whole poll.

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

### Basic Auth

```yaml
auth:
  type: "basic"
  username: "${STATLITE_ACTUATOR_USERNAME}"
  password: "${STATLITE_ACTUATOR_PASSWORD}"
```

Only `basic` is supported in the MVP. Prefer environment variables for credentials, so they are not stored in plaintext YAML. Export them before starting StatLite (or set them with your service manager):

```bash
export STATLITE_ACTUATOR_USERNAME="admin"
export STATLITE_ACTUATOR_PASSWORD="replace-with-a-secret"
statlite --config /etc/statlite/config.yaml
```

StatLite expands environment variables across the entire YAML file once at startup, before it parses the config. Both `$VAR` and `${VAR}` work; unset variables expand to an empty string. Shell-style defaults such as `${VAR:-default}` are not supported. Use `$${` for a literal `${` in the config. Restart StatLite after changing an environment variable.

Plaintext credentials still work as a fallback. Restrict config file permissions when they are present:

```bash
chmod 600 /etc/statlite/config.yaml
chown statlite:statlite /etc/statlite/config.yaml
```

StatLite strips credentials from source endpoints before showing them in the dashboard or API responses.

### StatLite self-monitoring

```yaml
targets:
  - name: "statlite-self"
    type: "statlite-metrics"
    url: "http://127.0.0.1:9090/statlite/metrics"
```

`type: "statlite-metrics"` polls another StatLite (or this process) via
`/statlite/metrics`, the canonical fixed `statlite-metrics/v1` profile. The
same profile is also available to supported external application integrations;
it is not a general metrics protocol.

For compatibility, an older `type: "statlite"` target is migrated at startup
to `type: "statlite-metrics"` and its URL path is changed to
`/statlite/metrics`. StatLite logs a deprecation warning; update the
configuration before a future release removes this migration.

### StatLite Metrics v1

```yaml
targets:
  - name: "python-demo"
    type: "statlite-metrics"
    url: "http://127.0.0.1:8000/statlite/metrics"
```

Applications using `type: statlite-metrics` expose the fixed
`statlite-metrics/v1` JSON profile. See [StatLite Metrics v1](statlite-metrics-v1.md)
for the complete response format, field semantics, and implementation guidance.
StatLite performs one bounded JSON GET per poll; Basic Auth is not part of v1.

Root `statlite.yaml` uses this pattern so Quick Start works with no extra config.

### Host resources

The `statlite-self` target reports the local host CPU, memory, and filesystem
containing StatLite's SQLite database through `/statlite/metrics`. It is the
single target for StatLite's application, process, and host-resource charts.

For a remote application, a central StatLite instance cannot obtain that
machine's host resources unless the application emits the optional host fields
in `statlite-metrics/v1` or another StatLite instance runs on the remote host.

## Dashboard URL state

Selected target and time range are stored in the query string, so you can bookmark a view:

```text
/?target=catalog-api&range=1h
```

## API notes

* `/api/*` is early/internal and not yet a stable public API.
* `/healthz` exposes process version and readiness. Monitored-target poll failures do not mark the process unhealthy; SQLite failure does (`status: "error"`, HTTP 503).

## Example files

| File | Purpose |
|------|---------|
| `statlite.yaml` (repo root) | Default Quick Start that monitors StatLite itself |
| `examples/actuator.yaml` | Single Spring Boot Actuator target with Basic Auth placeholders |
| `examples/statlite.yaml` | Monitor another StatLite instance with `statlite-metrics` |
| `examples/multi-target.yaml` | Illustrative multi-target mix (Actuator + StatLite Metrics + self) |
| `examples/python-fastapi-demo/` | Runnable FastAPI StatLite Metrics v1 demo |
| `examples/spring-actuator-demo/` | Standalone Spring Boot demo app that emits Actuator and Micrometer metrics |

## Systemd

A starter unit is in [statlite.service.example](statlite.service.example). Point `ExecStart` at your binary and config path. Installers do not install this unit automatically.
