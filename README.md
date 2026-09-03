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

Inspect an application's base URL to generate a minimal configuration:

```bash
statlite inspect 'http://localhost:8080'
```

For Quarkus, select the type and supply either its application base URL or exact
metrics URL:

```bash
statlite inspect --type quarkus 'http://localhost:9000'
```

Save the printed configuration as `statlite.yaml`, then run `statlite`. For an
existing configuration, copy only the generated target entry into its `targets`
list.

Inspection is bounded and read-only. See
[Configuration](docs/configuration.md) for supported URL forms, inspection
limits, manual configuration, and `--no-poll` mode for viewing existing history.
See [`examples/`](examples/) for complete configurations.

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
