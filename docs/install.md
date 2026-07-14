# Install StatLite

This document covers install and build paths for the public OSS binary.

Installers and package managers install only the `statlite` binary. They do not
create config files, initialize SQLite storage, install systemd units, create
users, or start services.

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
curl -fsSL https://raw.githubusercontent.com/PVRLabs/statlite/main/install.sh | STATLITE_VERSION=v0.1.0 sh
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

Windows installer artifacts are not part of the initial release.

## Homebrew

After the shared PVRLabs tap is populated, install with:

```bash
brew install pvrlabs/tap/statlite
```

The formula installs the binary from GitHub Releases. StatLite remains
installable without Homebrew.

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
go build -trimpath -ldflags="-s -w -X github.com/pvrlabs/statlite/internal/version.Version=v0.1.0" -o statlite ./cmd/statlite
```

## Run With Config

The default config path is `statlite.yaml` in the current working directory.
For an installed binary, copy an example or create your own config, then pass it
explicitly:

```bash
cp examples/actuator.yaml ./statlite.yaml
# edit URLs and credentials
statlite --config ./statlite.yaml
```

The config may contain Actuator credentials. Restrict it on servers:

```bash
chmod 600 ./statlite.yaml
```

For config fields and examples, see [configuration.md](configuration.md).

For a runnable Spring Boot demo app that StatLite can monitor, see `examples/spring-actuator-demo/`.

## Verify

Check the binary version:

```bash
statlite --version
```

Published installs should report the current release version. Source builds from
`main` may report the next development version, for example
`statlite v0.1.1-dev`, until the next release is prepared.

## Release Notes

For release publishing and artifact details, see [releasing.md](releasing.md).
