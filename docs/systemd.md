# Run StatLite as a systemd service

`install.sh` is the user-level installer: it installs only the StatLite binary,
normally in `~/.local/bin`. It does not create configuration, users, data
directories, or services. Use `scripts/systemd.sh` to provision a server-wide
systemd service on a Linux host by downloading its release-tagged copy.

The script must run as root. It downloads GitHub Release assets directly,
verifies their SHA-256 checksums, and keeps release logic in this repository so
a pinned release never fetches mutable installer code from `main`.

## Install

Choose the StatLite release to install:

```bash
STATLITE_VERSION=v0.1.0
```

Download the provisioning script from the same release tag:

```bash
curl -fsSL \
  "https://raw.githubusercontent.com/PVRLabs/statlite/${STATLITE_VERSION}/scripts/systemd.sh" \
  -o statlite-systemd.sh
```

Review the downloaded script, then run it with a complete StatLite
configuration:

```bash
sudo sh statlite-systemd.sh install \
  --version "${STATLITE_VERSION}" \
  --config ./statlite.yaml
```

Pinning both the script and `--version` ensures the provisioning logic and
StatLite binary come from the same release.

When running from a repository checkout, the equivalent command is:

```bash
sudo ./scripts/systemd.sh install \
  --version "${STATLITE_VERSION}" \
  --config ./statlite.yaml
```

`--config` is the caller-supplied source file to copy. It is not referenced in
place. `--config-path` is the managed destination the service reads.

| Setting | Default |
| --- | --- |
| Service user/group | `statlite` |
| Binary | `/usr/local/bin/statlite` |
| Config destination | `/etc/statlite/config.yaml` |
| Data/working directory | `/var/lib/statlite` |
| Unit | `/etc/systemd/system/statlite.service` |
| Version | latest release |

The default `statlite` system user and group are created automatically.
Existing custom users/groups are reused without unrelated account changes. A
missing custom user or group requires `--create-user`.

```bash
sudo sh statlite-systemd.sh install \
  --version "${STATLITE_VERSION}" \
  --user finrecord \
  --group finrecord \
  --binary-path /home/finrecord/.local/bin/statlite \
  --config ./generated/statlite.yaml \
  --config-path /etc/finrecord/statlite.yaml \
  --data-dir /var/lib/finrecord/statlite \
  --service-name finrecord-statlite \
  --no-start
```

This supports external provisioners such as FinRecord while keeping their
configuration generation outside this generic script. `--no-start` still
enables the service, but does not start it.

Configuration is `root:<service-group>` and `0640`. Its managed directory is
`root:<service-group>` and `0750`; the data directory is
`<service-user>:<service-group>` and `0750`. The unit uses the data directory
as its working directory, so relative SQLite paths resolve there.

## Safe reruns and unit reconciliation

Rerunning install is safe when layout and configuration match. It refreshes the
binary and keeps the service enabled. It never silently replaces a different
configuration, migrates paths, or overwrites local unit edits.

The generated unit records a management marker and layout metadata. If the
layout matches but the unit has local changes, explicitly replace only the unit:

```bash
sudo sh statlite-systemd.sh install --config ./statlite.yaml --reconcile-unit
```

Layout conflicts must be resolved by an operator; the script does not migrate
an installation implicitly. Reconciliation never changes configuration or data.

## Upgrade

Upgrades discover existing paths, account, and group from the managed unit:

```bash
sudo sh statlite-systemd.sh upgrade
sudo sh statlite-systemd.sh upgrade --version 1.2.3
sudo sh statlite-systemd.sh upgrade --no-restart
```

The new binary is downloaded, checksum-verified, and version-checked before it
replaces the current binary. The former binary is retained as
`<binary-path>.previous` for manual recovery. Configuration, data, and the unit
are unchanged by default. Use `--reconcile-unit` to intentionally refresh a
customized managed unit. It reloads systemd and restarts unless `--no-restart`
is also supplied.

For manual recovery, stop the service, move `<binary-path>.previous` back to
`<binary-path>`, then start it. Automatic rollback is not performed.

## Verify and troubleshoot

```bash
systemctl status statlite.service --no-pager
journalctl -u statlite.service -n 50 --no-pager
/usr/local/bin/statlite --version
```

If service startup fails, the script prints this status and the last 50 journal
entries, then exits nonzero. Check configuration syntax and permissions, SQLite
path access, and target network access.
