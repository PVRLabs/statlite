# StatLite

[![GitHub stars](https://img.shields.io/github/stars/PVRLabs/statlite?style=flat)](https://github.com/PVRLabs/statlite/stargazers)
[![GitHub release](https://img.shields.io/github/v/release/PVRLabs/statlite)](https://github.com/PVRLabs/statlite/releases)
[![License](https://img.shields.io/github/license/PVRLabs/statlite)](LICENSE)

A lightweight, self-hosted metrics dashboard with a small memory and operational
footprint, designed for applications running on VPSs and small servers. A single
Go binary monitors Spring Boot applications through Actuator and other
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

See [Installation](docs/install.md) for the release installer, Homebrew, and
source-build instructions.

## Configure an application

Start with an application's base URL. StatLite can identify a supported
integration and print a minimal, runnable `statlite.yaml`:

```bash
statlite inspect 'http://localhost:8080'
```

The command checks the conventional Spring Boot Actuator and StatLite Metrics
v1 paths, then prints the detected endpoint, available capabilities, a minimal
configuration, and the next command. It is bounded and read-only: it does not
require or create
`statlite.yaml`, open SQLite, or start monitoring.

> [!IMPORTANT]
> Quote browser-pasted URLs, especially URLs containing `?` or `&`. Inspection
> requires a base HTTP or HTTPS URL, so remove any query string or fragment
> first.

For a new Spring application, the successful output includes a complete
minimal configuration:

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
    actuator_base_url: "http://localhost:8080/actuator"
```

For a new setup, save the configuration as `statlite.yaml`. If you already
have a configuration, copy only the target entry into its `targets` list. Then
run:

```bash
statlite
```

See [Configuration](docs/configuration.md) for discovery limits, authentication
limitations, all settings, and manual target configuration. See [`examples/`](examples/)
for Spring Boot, Python/FastAPI, self-monitoring, and multi-target configurations.

> [!IMPORTANT]
> StatLite has no built-in dashboard or API authentication. Review the
> [server and access guidance](docs/configuration.md#server) before exposing it
> remotely.

## Supported metric sources

- **Spring Boot Actuator:** Collects health, request, JVM, process, and optional
  host metrics from Actuator endpoints.
- **[StatLite Metrics v1](docs/statlite-metrics-v1.md):** A small, fixed JSON
  endpoint that applications in any language or framework can implement.
- **StatLite self-monitoring:** StatLite can report its own health, traffic,
  process, and host metrics.

Depending on the integration, StatLite can also collect CPU, memory, and disk
metrics for the environment running the application.

## Documentation

- [Installation](docs/install.md)
- [Docker](docs/docker.md)
- [Configuration](docs/configuration.md)
- [Monitoring on resource-constrained servers](docs/low-resource-monitoring.md)
- [StatLite Metrics v1](docs/statlite-metrics-v1.md)
- [systemd deployment](docs/systemd.md)
- [Product and architecture](docs/product.md)
- [Spring Boot guide](https://pvrlabs.xyz/articles/lightweight-spring-boot-monitoring.html)
- [Examples](examples/)

## License

MIT
