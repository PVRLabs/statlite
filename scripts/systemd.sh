#!/bin/sh
# Managed systemd provisioning for StatLite. Release logic lives in this
# repository, so pinned release installs do not fetch mutable installer code.
set -eu
repo="${STATLITE_REPO:-PVRLabs/statlite}"
fail(){ echo "statlite systemd: $*" >&2; exit 1; }
need(){ command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"; }
help(){ cat <<'EOF'
Usage:
  sudo ./scripts/systemd.sh install --config <source-file> [options]
  sudo ./scripts/systemd.sh upgrade [options]
  sudo ./scripts/systemd.sh help

Install options:
  --config <source-file>       Configuration source to copy (required)
  --config-path <path>         Managed configuration path (/etc/statlite/config.yaml)
  --user <name>                Service user (statlite)
  --group <name>               Service group (same as --user)
  --binary-path <path>         Binary path (/usr/local/bin/statlite)
  --data-dir <path>            Data/working directory (/var/lib/statlite)
  --service-name <name>        systemd service name (statlite)
  --version <version>          Release version (latest)
  --create-user                Create a missing custom user/group
  --reconcile-unit             Replace a managed unit with local changes
  --no-start                   Enable but do not start the service

Upgrade options:
  --service-name <name>        systemd service name (statlite)
  --version <version>          Release version (latest)
  --reconcile-unit             Replace a locally customized managed unit
  --no-restart                 Replace binary without restarting the service
EOF
}
name(){ case "$2" in ''|*[!A-Za-z0-9_.-]*) fail "invalid $1: $2";; esac; }
path(){ case "$2" in /*) ;; *) fail "$1 must be absolute: $2";; esac; case "$2" in *' '*|*'	'*) fail "$1 must not contain whitespace: $2";; esac; }
layout(){ name "service name" "$service_name"; name user "$service_user"; name group "$service_group"; path "binary path" "$binary_path"; path "config path" "$config_path"; path "data directory" "$data_dir"; }
unit_path(){ echo "/etc/systemd/system/$service_name.service"; }
unit(){ cat <<EOF
# Managed by StatLite scripts/systemd.sh
# StatLite-Binary-Path: $binary_path
# StatLite-Config-Path: $config_path
# StatLite-Data-Dir: $data_dir
# StatLite-User: $service_user
# StatLite-Group: $service_group
[Unit]
Description=StatLite metrics dashboard
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$service_user
Group=$service_group
WorkingDirectory=$data_dir
ExecStart=$binary_path --config $config_path
Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
EOF
}
value(){ sed -n "s|^# StatLite-$1: ||p" "$managed_unit" | sed -n '1p'; }
owner_mode(){ stat -c '%U:%G %a' "$1" 2>/dev/null || stat -f '%Su:%Sg %Lp' "$1"; }
directory(){ d="$1"; o="$2"; m="$3"; if [ ! -e "$d" ]; then mkdir -p "$d"; chown "$o" "$d"; chmod "$m" "$d"; fi; [ -d "$d" ] || fail "path is not a directory: $d"; a="$(owner_mode "$d")"; [ "$a" = "$o $m" ] || fail "directory has incompatible ownership or permissions: $d is $a; expected $o $m"; }
account(){
  if ! getent group "$service_group" >/dev/null 2>&1; then [ "$service_group" = statlite ] || [ "$create_user" = true ] || fail "custom group does not exist: $service_group; rerun with --create-user"; groupadd --system "$service_group"; fi
  if ! id "$service_user" >/dev/null 2>&1; then [ "$service_user" = statlite ] || [ "$create_user" = true ] || fail "custom user does not exist: $service_user; rerun with --create-user"; useradd --system --gid "$service_group" --no-create-home --shell /usr/sbin/nologin "$service_user"; fi
}
semver(){ printf '%s\n' "$1" | awk '/^[0-9]+\.[0-9]+\.[0-9]+$/ { valid = 1 } END { exit !valid }'; }
release(){
  release_version="$1"; case "$release_version" in latest) release_version="$(curl -fsSLo /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest")"; release_version="$(basename "$release_version")";; esac
  number="$(echo "$release_version" | sed 's/^v//')"; semver "$number" || fail "invalid version: $release_version. Expected latest or X.Y.Z"; release_version="v$number"; archive="statlite_$number"_linux_"$arch".tar.gz; url="https://github.com/$repo/releases/download/$release_version/$archive"
  echo "Downloading StatLite $release_version for linux/$arch..."
  curl -fsSL "$url" -o "$tmp_dir/$archive"; curl -fsSL "$url.sha256" -o "$tmp_dir/$archive.sha256"
  expected="$(awk '{print $1; exit}' "$tmp_dir/$archive.sha256")"; [ -n "$expected" ] || fail "checksum file is empty: $url.sha256"
  if command -v sha256sum >/dev/null 2>&1; then actual="$(sha256sum "$tmp_dir/$archive" | awk '{print $1; exit}')"; else actual="$(shasum -a 256 "$tmp_dir/$archive" | awk '{print $1; exit}')"; fi
  [ "$actual" = "$expected" ] || fail "checksum mismatch for $archive"
  tar -xzf "$tmp_dir/$archive" -C "$tmp_dir" statlite; candidate="$tmp_dir/statlite"; [ -f "$candidate" ] || fail "archive did not contain statlite"; chmod 0755 "$candidate"
  [ "$("$candidate" --version)" = "statlite $release_version" ] || fail "downloaded binary did not report statlite $release_version"
}
put_binary(){
  d="$(dirname "$binary_path")"; [ -d "$d" ] || mkdir -p "$d"; staged="$(mktemp "$d/.statlite.new.XXXXXX")"; cp "$candidate" "$staged"; chown root:root "$staged"; chmod 0755 "$staged"
  if [ "$1" = upgrade ] && [ -f "$binary_path" ]; then mv -f "$binary_path" "$binary_path.previous"; fi
  mv -f "$staged" "$binary_path"
  if [ "$("$binary_path" --version)" != "statlite $release_version" ]; then
    if [ "$1" = upgrade ] && [ -f "$binary_path.previous" ]; then
      mv -f "$binary_path" "$binary_path.failed"
      mv -f "$binary_path.previous" "$binary_path"
      fail "installed binary version verification failed; restored the previous binary (failed candidate saved as $binary_path.failed)"
    fi
    fail "installed binary version verification failed"
  fi
}
preflight_unit(){
  generated="$tmp_dir/$service_name.service"; unit > "$generated"; target="$(unit_path)"
  [ -e "$target" ] || return 0
  cmp -s "$generated" "$target" && return
  grep -Fqx '# Managed by StatLite scripts/systemd.sh' "$target" || fail "service name $service_name belongs to an unrelated unit: $target"
  managed_unit="$target"; ob="$(value Binary-Path)"; oc="$(value Config-Path)"; od="$(value Data-Dir)"; ou="$(value User)"; og="$(value Group)"
  [ "$ob" = "$binary_path" ] || fail "existing unit uses binary path ${ob:-<missing>}, requested $binary_path; layout migration is not automatic"
  [ "$oc" = "$config_path" ] || fail "existing unit uses config path ${oc:-<missing>}, requested $config_path; layout migration is not automatic"
  [ "$od" = "$data_dir" ] || fail "existing unit uses data directory ${od:-<missing>}, requested $data_dir; layout migration is not automatic"
  [ "$ou" = "$service_user" ] && [ "$og" = "$service_group" ] || fail "existing unit uses a different service user or group; layout migration is not automatic"
  [ "$reconcile_unit" = true ] || fail "existing managed unit has local changes; rerun with --reconcile-unit to replace it"
}
write_unit(){
  [ -n "${generated:-}" ] && [ -n "${target:-}" ] || fail "internal error: systemd unit was not preflighted"
  install -m 0644 "$generated" "$target"
}
diagnostics(){ systemctl status "$service_name.service" --no-pager >&2 || true; journalctl -u "$service_name.service" -n 50 --no-pager >&2 || true; }
restart(){ if ! systemctl restart "$service_name.service" || ! systemctl is-active --quiet "$service_name.service"; then echo "StatLite service failed to start; diagnostics follow." >&2; diagnostics; fail "service is not active"; fi; }

command="${1:-help}"; case "$command" in install|upgrade) shift;; help|--help|-h) help; exit 0;; *) help >&2; fail "unknown command: $command";; esac
service_name=statlite; version_name=latest; config_source=; config_path=/etc/statlite/config.yaml; binary_path=/usr/local/bin/statlite; data_dir=/var/lib/statlite; service_user=statlite; service_group=; group_set=false; create_user=false; reconcile_unit=false; no_start=false; no_restart=false
while [ "$#" -gt 0 ]; do
  case "$1" in
    --config|--config-path|--user|--group|--binary-path|--data-dir|--service-name|--version) [ "$#" -ge 2 ] || fail "missing value for $1"; k="$1"; v="$2"; shift 2; case "$k" in --config) config_source="$v";; --config-path) config_path="$v";; --user) service_user="$v";; --group) service_group="$v"; group_set=true;; --binary-path) binary_path="$v";; --data-dir) data_dir="$v";; --service-name) service_name="$v";; --version) version_name="$v";; esac;;
    --create-user) create_user=true; shift;; --reconcile-unit) reconcile_unit=true; shift;; --no-start) no_start=true; shift;; --no-restart) no_restart=true; shift;; *) fail "unknown option: $1";;
  esac
done
[ "$group_set" = true ] || service_group="$service_user"
if [ "$command" = install ]; then [ -n "$config_source" ] || fail "install requires --config <source-file>"; [ "$no_restart" = false ] || fail "--no-restart is only valid with upgrade"; [ -f "$config_source" ] && [ -r "$config_source" ] || fail "configuration source is not a readable regular file: $config_source"; layout
else [ -z "$config_source" ] || fail "--config is only valid with install"; [ "$no_start" = false ] || fail "--no-start is only valid with install"; [ "$create_user" = false ] || fail "--create-user is only valid with install"; [ "$config_path" = /etc/statlite/config.yaml ] && [ "$binary_path" = /usr/local/bin/statlite ] && [ "$data_dir" = /var/lib/statlite ] && [ "$service_user" = statlite ] && [ "$group_set" = false ] || fail "upgrade discovers layout from the managed unit; only --service-name and --version are accepted"; name "service name" "$service_name"; fi
[ "$(id -u)" -eq 0 ] || fail "must be run as root"; [ "$(uname -s)" = Linux ] || fail "systemd provisioning is supported only on Linux"; need systemctl; [ -d /run/systemd/system ] || fail "this host does not appear to be running systemd"; systemctl --version >/dev/null 2>&1 || fail "systemctl is not usable on this host"
for c in curl tar mktemp awk sed grep cmp install getent groupadd useradd dirname basename; do need "$c"; done
case "$(uname -m)" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) fail "unsupported architecture: $(uname -m)";; esac
tmp_dir="$(mktemp -d)"; trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM
if [ "$command" = install ]; then
  preflight_unit
  account; directory "$(dirname "$config_path")" "root:$service_group" 750; directory "$data_dir" "$service_user:$service_group" 750
  if [ -e "$config_path" ] && ! cmp -s "$config_source" "$config_path"; then fail "managed configuration differs from --config source: $config_path; configuration replacement is intentionally not supported"; fi
  if [ ! -e "$config_path" ]; then install -m 0640 -o root -g "$service_group" "$config_source" "$config_path"; fi
  [ "$(owner_mode "$config_path")" = "root:$service_group 640" ] || fail "configuration has incompatible ownership or permissions: $config_path is $(owner_mode "$config_path"); expected root:$service_group 640"
  release "$version_name"; put_binary install; write_unit; systemctl daemon-reload; systemctl enable "$service_name.service"; [ "$no_start" = true ] || restart; echo "StatLite $release_version installed for $service_name.service"
else
  managed_unit="$(unit_path)"; [ -f "$managed_unit" ] || fail "managed unit not found: $managed_unit"; grep -Fqx '# Managed by StatLite scripts/systemd.sh' "$managed_unit" || fail "$managed_unit is not managed by this script; refusing to modify it"
  binary_path="$(value Binary-Path)"; config_path="$(value Config-Path)"; data_dir="$(value Data-Dir)"; service_user="$(value User)"; service_group="$(value Group)"; [ -n "$binary_path" ] && [ -n "$config_path" ] && [ -n "$data_dir" ] && [ -n "$service_user" ] && [ -n "$service_group" ] || fail "$managed_unit is missing StatLite layout metadata"; layout
  release "$version_name"; put_binary upgrade
  if [ "$reconcile_unit" = true ]; then unit > "$tmp_dir/$service_name.service"; install -m 0644 "$tmp_dir/$service_name.service" "$managed_unit"; systemctl daemon-reload; fi
  [ "$no_restart" = true ] || restart; echo "StatLite $release_version upgraded for $service_name.service"
fi
