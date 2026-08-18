# Docker

StatLite publishes one multi-platform image for `linux/amd64` and `linux/arm64`:

```text
ghcr.io/pvrlabs/statlite:latest
```

## Run the demo

```bash
docker run --rm \
  -p 127.0.0.1:9090:9090 \
  ghcr.io/pvrlabs/statlite:latest
```

Open <http://127.0.0.1:9090>. The image includes a default configuration that
monitors StatLite itself through `statlite-metrics`; no configuration file is
required.

> [!NOTE]
> The bundled configuration polls every 30 seconds, a production-sensible
> default that limits HTTP requests, SQLite writes, and database growth. The
> first poll runs immediately. When it establishes a new counter baseline,
> either for a new database or a new application run, StatLite makes one
> follow-up poll after three seconds so charts can establish their first delta,
> then returns to the configured interval.

The container stores SQLite data at `/data/statlite.sqlite`. Data is ephemeral
unless `/data` is mounted to persistent storage.

## Access and metrics

StatLite has no built-in dashboard or API authentication. The example publishes
port 9090 only on host loopback so the dashboard is not exposed directly to the
network. Use appropriate external access controls before publishing it more
broadly.

Container resource metrics describe the environment visible inside the
container, not the physical macOS host.

The image runs as the non-root `statlite` user and includes CA certificates for
HTTPS targets. Dashboard assets and the SQLite schema are embedded in the
binary.

## Build locally

From the repository root:

```bash
docker build \
  --build-arg VERSION=dev \
  -t statlite:local \
  .
```

Check the embedded version:

```bash
docker run --rm statlite:local --version
```

For local development, the bundled image configuration listens on
`0.0.0.0:9090` inside the container and the example above restricts access at
the host port.

Application examples are available under `examples/`, including the Spring
Boot and Python FastAPI demos.

## Maintainer pre-release image

Maintainers may publish a temporary `:dev` image for pre-release verification.
Build and inspect it from the repository root:

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --build-arg VERSION=dev \
  --tag ghcr.io/pvrlabs/statlite:dev \
  --push \
  .

docker buildx imagetools inspect ghcr.io/pvrlabs/statlite:dev
```

See [Releasing StatLite](releasing.md) for publishing versioned and `:latest`
images.
