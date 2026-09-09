# StatLite on TierHive

This recipe installs one StatLite instance on a native Alpine Linux TierHive
VPS. It creates an OpenRC service that runs as the non-root `statlite` user and
stores its configuration and SQLite history on the VPS.

## Fresh installation

Run the recipe as root and provide the Spring Boot Actuator base URL through the
lower-case `spring_actuator_url` input. On a minimal Alpine VPS, use the
provider console or another available root-login method. `sudo -i` is only an
optional way to become root when sudo is installed. The URL is written to the
normal StatLite `targets[].url` field. It is not the deprecated
`targets[].actuator_base_url` field.

The shortest curlable example is unauthenticated and contains no password:

```sh
export spring_actuator_url='http://127.0.0.1:8080/actuator'
curl -fsSL https://raw.githubusercontent.com/PVRLabs/statlite/main/deploy/tierhive/run.sh | sh
```

For an authenticated Actuator endpoint, enter credentials interactively in a
root shell so the password is not placed in shell history or printed:

```sh
# Optional when sudo is installed: sudo -i
printf 'Actuator username: '
read -r spring_auth_username
printf 'Actuator password: '
read -rs spring_auth_password
printf '\n'
export spring_auth_username spring_auth_password
export spring_actuator_url='http://127.0.0.1:8080/actuator'
curl -fsSL https://raw.githubusercontent.com/PVRLabs/statlite/main/deploy/tierhive/run.sh | sh
```

The fresh-install inputs are:

| Input | Required | Default or meaning |
| --- | --- | --- |
| `spring_actuator_url` | Yes | Spring Boot Actuator base URL, for example `http://127.0.0.1:8080/actuator` |
| `spring_app_name` | No | `my-spring-app` |
| `spring_auth_username` | No | Basic Auth username |
| `spring_auth_password` | No | Basic Auth password |
| `statlite_version` | No | Latest release, resolved once to a concrete `vX.Y.Z` |

The username and password must be supplied together. The generated targets
contain one user-configured Spring target with `metrics_source: "auto"` and a
fixed local `statlite-self` target at
`http://127.0.0.1:9090/statlite/metrics`. Both use the standard `30s` polling
interval. Retention and timeout are omitted so StatLite applies its normal
defaults. See the [configuration reference](../../docs/configuration.md) for
the complete target schema.

Because StatLite expands environment variables before parsing YAML, the recipe
rejects `$` in `spring_actuator_url` and `spring_app_name`. This prevents a
directly embedded input from changing during that expansion. Credential values
are stored through the separate credential environment mechanism and may
contain `$`.

For an explicit release, set `statlite_version` to the exact `vX.Y.Z` tag. The
recipe fetches the installer from that tag, passes the same version to the
installer, reuses the installer's release archive checksum verification, and
checks the installed binary's reported version. An unpinned install resolves
the current release once and passes that concrete version to the current
installer.

## Reruns and files

If `/etc/statlite/statlite.yaml` already exists, the recipe does not require
any Spring target inputs. That configuration, and the existing credential
environment when present, are authoritative. New Spring inputs are ignored.
Reruns may refresh the verified binary, account permissions, OpenRC service,
and service state, but do not replace the configuration, credentials, SQLite
database, WAL, SHM, or history.

The deployment uses these fixed paths:

| Path | Purpose |
| --- | --- |
| `/usr/local/bin/statlite` | Verified StatLite binary |
| `/etc/statlite/statlite.yaml` | Normal StatLite configuration, `root:statlite`, mode `0640` |
| `/etc/conf.d/statlite` | Exported credential values when Basic Auth is configured, `root:root`, mode `0600` |
| `/var/lib/statlite/statlite.sqlite` | Persistent SQLite database, including its WAL and SHM files |
| `/var/log/statlite/statlite.log` | OpenRC standard output log |
| `/var/log/statlite/statlite.err` | OpenRC error log |
| `/etc/init.d/statlite` | OpenRC service definition |

The dashboard listens on `127.0.0.1:9090`. It is not publicly exposed by the
recipe. Credentials are represented in YAML only by environment references;
the resolved values are kept in the mode `0600` OpenRC environment file and
are not passed in command arguments. Do not print that file or add it to a
world-readable backup.

The recipe does not require the first Actuator poll to succeed before
provisioning reports success. The local StatLite `/healthz` check verifies the
StatLite process and SQLite readiness. A target that is unreachable can still
allow this local check to pass, with target status visible after the service
starts.

## Service and status checks

Use OpenRC to inspect and operate the service:

```sh
rc-service statlite status
rc-status
rc-update show | grep statlite
rc-service statlite restart
rc-service statlite stop
rc-service statlite start
```

Inspect recent output without displaying the configuration or credential
environment:

```sh
tail -n 50 /var/log/statlite/statlite.log
tail -n 50 /var/log/statlite/statlite.err
curl -fsS http://127.0.0.1:9090/healthz
```

On a fresh install, and on reruns that preserve the recipe's exact
`127.0.0.1:9090` listener, the recipe performs a bounded local health check.
If an existing configuration uses another listener, it is preserved and the
automatic check is skipped. After confirming that listener, use
`curl -fsS http://HOST:PORT/healthz` manually. The recipe does not parse or
rewrite arbitrary preserved YAML.

## Safe dashboard access

TierHive's first mapped external TCP port maps to guest SSH port 22. Use that
mapped SSH port to create an intentional local tunnel, replacing the
placeholders with the values from the TierHive VPS:

```sh
ssh -o ExitOnForwardFailure=yes -p <mapped-ssh-port> \
  -N -L 9090:127.0.0.1:9090 root@<tierhive-host>
```

Then open <http://127.0.0.1:9090> on the local machine. StatLite has no
built-in dashboard authentication. Keep the loopback listener, or separately
configure an authenticated proxy if remote access is needed. This exploratory
recipe does not configure HAProxy, public port publication, or proxy
authentication. See TierHive's [mapped-port guidance](https://tierhive.com/blog/tierhive-howto/tierhive-vps-ports)
for the platform-side port details.

## Actuator networking

Prefer `http://127.0.0.1:8080/actuator` when the Spring application runs on the
same VPS. For another TierHive instance, use its private network address, for
example `http://10.x.x.x:8080/actuator`. The monitored application can remain
private; it does not need a publicly exposed Actuator endpoint. Confirm that
the TierHive private network and the application's listener allow the
StatLite VPS to connect.

## Adding targets

The recipe generates one Spring target and the fixed local `statlite-self`
target. To add another target later, edit the normal YAML configuration and
add another entry under `targets:`. For example:

```yaml
targets:
  - name: "my-spring-app"
    type: "spring"
    url: "http://127.0.0.1:8080/actuator"
    metrics_source: "auto"
  - name: "another-spring-app"
    type: "spring"
    url: "http://10.0.0.3:8080/actuator"
    metrics_source: "auto"
  - name: "statlite-self"
    type: "statlite-metrics"
    url: "http://127.0.0.1:9090/statlite/metrics"
```

Use the ordinary StatLite `targets:` schema, including the documented `url`
field, then restart the service. A later recipe rerun preserves these manual
changes as authoritative. For target types and optional settings, consult the
[StatLite configuration reference](../../docs/configuration.md).
