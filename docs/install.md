# Install StatLite

This document covers installation, container, and build options for StatLite.

Curl and Homebrew install only the StatLite binary. They do not create
configuration files or start services. Docker provides a ready-to-run
self-monitoring demo.

For a server-wide Linux systemd installation, including service user,
configuration, data-directory, and unit provisioning, see [systemd.md](systemd.md).

## Curl Installer

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/PVRLabs/statlite/main/install.sh | sh
```

The installer downloads the matching GitHub Release archive for your platform,
verifies its SHA-256 checksum, and installs `statlite` into `~/.local/bin` by
default. If that directory is not on your `PATH`, add it before running
`statlite`.

Install a specific release:

```bash
curl -fsSL https://raw.githubusercontent.com/PVRLabs/statlite/main/install.sh | STATLITE_VERSION=v0.6.0 sh
```

Install into a custom directory:

```bash
curl -fsSL https://raw.githubusercontent.com/PVRLabs/statlite/main/install.sh | STATLITE_INSTALL_DIR="$HOME/bin" sh
```

Supported installer platforms:

- macOS `amd64`
- macOS `arm64`
- Linux `amd64`
- Linux `arm64`

## Windows

For Windows 10 or later on amd64 (x64), download
`statlite_<version>_windows_amd64.zip` and its `.sha256` file from
[GitHub Releases](https://github.com/PVRLabs/statlite/releases/latest).
Extract the zip into a writable folder, open PowerShell there, and run:

```powershell
.\statlite.exe --config .\statlite.yaml
```

Open <http://127.0.0.1:9090>. The zip includes `readme.txt` and a complete
self-monitoring configuration. See [Windows](windows.md) for checksum checking,
application monitoring, paths, and platform limitations.

## Homebrew

Install with:

```bash
brew install pvrlabs/tap/statlite
```

The formula installs the binary from GitHub Releases. StatLite remains
installable without Homebrew.

## Docker

Run the self-monitoring demo:

```bash
docker run --rm -p 127.0.0.1:9090:9090 docker.io/pvrlabs/statlite:latest
```

Open <http://127.0.0.1:9090>. See [Docker](docker.md) for application
configuration, persistent storage, and available image tags.

## Build From Source

From a clone:

```bash
go build -o statlite ./cmd/statlite
```

Run with the root self-monitoring config:

```bash
./statlite
```

Release-style local build with an explicit version:

```bash
go build -trimpath -ldflags="-s -w -X github.com/pvrlabs/statlite/internal/version.Version=v0.6.0" -o statlite ./cmd/statlite
```

## Run With Config

The default config path is `statlite.yaml` in the current working directory.
The installer does not include the `examples/` directory.

With a running Spring Boot application, write a new config and start:

```bash
statlite inspect 'http://localhost:8080' --create-config ./statlite.yaml
statlite
```

Open <http://127.0.0.1:9090>. `--create-config` writes the file only when that
path does not already exist. Quarkus, Micronaut, and
[StatLite Metrics applications](integrate/) use the same command with the type
or URL their guide shows. The written file sets `server.listen`,
`storage.sqlite_path`, and `polling.interval`. All three are required.

The config may contain Actuator credentials. Restrict it on servers:

```bash
chmod 600 ./statlite.yaml
```

For every config field, see [configuration.md](configuration.md). Example files
in a source checkout are listed in [`examples/`](../examples/).

For a runnable Spring Boot demo app that StatLite can monitor, see
`examples/spring-actuator-demo/` in the source repository.

## Verify

Check the binary version:

```bash
statlite --version
```

Published installs should report the current release version. Source builds from
`main` may report the next development version, for example
`statlite v0.6.1-dev`, until the next release is prepared.

## Release Notes

See the [changelog](../CHANGELOG.md) and published
[release notes and artifacts](https://github.com/PVRLabs/statlite/releases).
