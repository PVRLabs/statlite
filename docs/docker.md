# Docker

## Container images

StatLite distributes equivalent release images for `linux/amd64` and
`linux/arm64` through [Docker Hub](https://hub.docker.com/r/pvrlabs/statlite)
and [GHCR](https://github.com/PVRLabs/statlite/pkgs/container/statlite):

```text
ghcr.io/pvrlabs/statlite:latest
docker.io/pvrlabs/statlite:latest
```

Both registries provide versioned tags without the `v` prefix, such as
`0.6.0`, and `latest`. GHCR is the authoritative publishing source. After its
release checks pass, the release workflow copies the existing image to Docker
Hub without rebuilding and verifies both architectures, release identity,
anonymous pulls, startup, and `/healthz`.

Docker Hub pull counts measure registry pulls, not unique installations.
CI, retries, updates, and multiple machines can produce repeated pulls.

## Run the demo

```bash
docker run --rm \
  -p 127.0.0.1:9090:9090 \
  docker.io/pvrlabs/statlite:latest
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
unless `/data` is mounted to persistent storage. See
[Monitor an application](#monitor-an-application).

## Monitor an application

The demo above polls StatLite itself. To poll your application, mount a
config over `/etc/statlite/statlite.yaml` and mount `/data` when you want to
keep history.

Inside the container, `127.0.0.1` is StatLite. The published dashboard is
reachable when that config listens on `0.0.0.0:9090`. Keep the published port
on host loopback, as in the command below.

Point the target at `host.docker.internal` and the application port. Bind the
application on an address that interface can reach. A process listening only
on the host loopback is not reachable at that name. Docker Desktop resolves
`host.docker.internal`. On Linux, `--add-host` below provides the name.

```yaml
server:
  listen: "0.0.0.0:9090"

storage:
  sqlite_path: "/data/statlite.sqlite"

polling:
  interval: "30s"

targets:
  - name: "app"
    type: "spring"
    url: "http://host.docker.internal:8080/actuator"
```

For Express, Django, FastAPI, Go `net/http`, and Gin, use
`type: statlite-metrics` and the `/statlite/metrics` URL from the
[integration guides](integrate/). A file written on the host by
`statlite inspect --create-config` uses `127.0.0.1:9090` and
`./statlite.sqlite`. Before mounting that file, set `server.listen` to
`0.0.0.0:9090` and `storage.sqlite_path` to `/data/statlite.sqlite`, and point
the target URL at `host.docker.internal`.

```bash
docker run --rm \
  -p 127.0.0.1:9090:9090 \
  --add-host=host.docker.internal:host-gateway \
  -v "$PWD/statlite.yaml:/etc/statlite/statlite.yaml:ro" \
  -v statlite-data:/data \
  docker.io/pvrlabs/statlite:latest
```

You can also add the self-monitoring target to this `targets` list. Its URL
remains `http://127.0.0.1:9090/statlite/metrics` because that address is
inside the container.

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
