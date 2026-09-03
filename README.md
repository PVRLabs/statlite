# StatLite

[![GitHub stars](https://img.shields.io/github/stars/PVRLabs/statlite?style=flat)](https://github.com/PVRLabs/statlite/stargazers)
[![GitHub release](https://img.shields.io/github/v/release/PVRLabs/statlite)](https://github.com/PVRLabs/statlite/releases)
[![License](https://img.shields.io/github/license/PVRLabs/statlite)](LICENSE)
[![CI](https://github.com/PVRLabs/statlite/actions/workflows/test.yml/badge.svg)](https://github.com/PVRLabs/statlite/actions/workflows/test.yml)

A lightweight, self-hosted metrics dashboard with a small memory and operational
footprint, designed for applications running on VPSs and small servers. A single
Go binary monitors Spring Boot applications through Actuator JSON or Micrometer
Prometheus metrics, Quarkus applications through Micrometer metrics, and other
applications that expose [a small, fixed JSON metrics
endpoint](docs/statlite-metrics-v1.md), without requiring Prometheus or Grafana.
It stores focused health, traffic, latency, CPU, memory, and optional host metrics
in SQLite.

**Website:** [pvrlabs.xyz/statlite](https://pvrlabs.xyz/statlite)

![StatLite example dashboard](docs/images/dashboard.webp)

StatLite is built for [resource-constrained servers](docs/low-resource-monitoring.md).
Low memory, CPU, disk, and operational overhead are treated as product
constraints.

Learn how to set up [lightweight Spring Boot monitoring without Prometheus and Grafana](https://pvrlabs.xyz/articles/lightweight-spring-boot-monitoring.html).

## Try it

```bash
docker run --rm \
  -p 127.0.0.1:9090:9090 \
  ghcr.io/pvrlabs/statlite:latest
```

Open <http://127.0.0.1:9090>. StatLite monitors itself by default, so the
dashboard starts with live data.

See the [Docker guide](docs/docker.md) for persistent storage, container
networking, local builds, and access guidance.

StatLite is intentionally focused and is not a replacement for Prometheus and
Grafana.

## Install

Install the latest release on macOS or Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/PVRLabs/statlite/main/install.sh | sh
```

Or install with Homebrew:

```bash
brew install pvrlabs/tap/statlite
```

See [Installation](docs/install.md) for supported platforms, custom install
locations, source builds, and server-wide Linux setup.

## Configure an application

To monitor a Spring Boot application, create a minimal `statlite.yaml`:

```yaml
server:
  listen: "127.0.0.1:9090"

storage:
  sqlite_path: "./statlite.sqlite"

polling:
  interval: "30s"

targets:
  - name: "app"
    type: "spring"
    url: "http://localhost:8080/actuator"
```

For a new setup, save the configuration as `statlite.yaml`. If you already
have a configuration, copy only the target entry into its `targets` list. Then
run:

```bash
statlite
```

Alternatively, inspect a running application's base URL to generate the
configuration:

```bash
statlite inspect 'http://localhost:8080'
```

For other supported frameworks, select the type explicitly when needed:

```bash
statlite inspect --type quarkus 'http://localhost:9000'
```

Inspection checks conventional supported endpoints and is bounded and
read-only. For untyped discovery, start with a base HTTP or HTTPS URL without a
query string or fragment.

See [Configuration](docs/configuration.md) for exact endpoint forms, discovery
limits, authentication limitations, all settings, and manual target
configuration. See [`examples/`](examples/) for complete configurations.

> [!IMPORTANT]
> StatLite has no built-in dashboard or API authentication. Review the
> [server and access guidance](docs/configuration.md#server) before exposing it
> remotely.

## Supported metric sources

- **Spring Boot:** Collects health through Actuator and automatically selects a
  compatible Micrometer Prometheus endpoint or Actuator JSON for request, JVM,
  process, and optional host metrics.
- **Quarkus Micrometer:** Collects bounded request, latency, CPU, heap, process,
  and restart concepts from an exact Prometheus/OpenMetrics endpoint. SmallRye
  Health is an optional capability when the application publishes it.
- **[StatLite Metrics v1](docs/statlite-metrics-v1.md):** A small, fixed JSON
  endpoint that applications in any language or framework can implement.
- **StatLite self-monitoring:** StatLite can report its own health, traffic,
  process, and host metrics.

Use a `statlite-self` target for host metrics where StatLite runs. Spring
targets can optionally collect host CPU and disk metrics for a remote
application's environment.

## Documentation

- [Installation](docs/install.md)
- [Docker](docs/docker.md)
- [Configuration](docs/configuration.md)
- [Deprecations and compatibility](docs/deprecations.md)
- [Supported integrations](docs/integrations.md)
- [Monitoring on resource-constrained servers](docs/low-resource-monitoring.md)
- [StatLite Metrics v1](docs/statlite-metrics-v1.md)
- [systemd deployment](docs/systemd.md)
- [Product and architecture](docs/product.md)
- [Storage and schema direction](docs/storage.md)
- [Spring Boot guide](https://pvrlabs.xyz/articles/lightweight-spring-boot-monitoring.html)
- [Examples](examples/)

## License

MIT
