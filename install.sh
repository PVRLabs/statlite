#!/bin/sh
set -eu

repo="${STATLITE_REPO:-PVRLabs/statlite}"
install_dir="${STATLITE_INSTALL_DIR:-}"
version="${STATLITE_VERSION:-}"
binary_name="statlite"

fail() {
  echo "statlite installer: $*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

need curl
need tar
need mktemp
need uname
need awk

case "$(uname -s)" in
  Darwin) os="darwin" ;;
  Linux) os="linux" ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) arch="amd64" ;;
  arm64 | aarch64) arch="arm64" ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

if [ -z "$version" ]; then
  latest_url="$(curl -fsSLo /dev/null -w '%{url_effective}' "https://github.com/${repo}/releases/latest")"
  version="${latest_url##*/}"
fi

case "$version" in
  v[0-9]*.[0-9]*.[0-9]*) ;;
  *) fail "invalid version: ${version}. Expected a tag like v0.1.0" ;;
esac

version_number="${version#v}"
archive_name="${binary_name}_${version_number}_${os}_${arch}.tar.gz"
base_url="https://github.com/${repo}/releases/download/${version}"
archive_url="${base_url}/${archive_name}"
checksum_url="${archive_url}.sha256"

if [ -z "$install_dir" ]; then
  [ -n "${HOME:-}" ] || fail "HOME is not set; set STATLITE_INSTALL_DIR to choose an install directory"

  if mkdir -p "${HOME}/.local/bin" 2>/dev/null; then
    install_dir="${HOME}/.local/bin"
  elif mkdir -p "${HOME}/bin" 2>/dev/null; then
    install_dir="${HOME}/bin"
  else
    fail "could not create ${HOME}/.local/bin or ${HOME}/bin; set STATLITE_INSTALL_DIR"
  fi
else
  mkdir -p "$install_dir" || fail "could not create install directory: ${install_dir}"
fi

[ -d "$install_dir" ] || fail "install target is not a directory: ${install_dir}"
[ -w "$install_dir" ] || fail "install target is not writable: ${install_dir}"

tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

archive_path="${tmp_dir}/${archive_name}"
checksum_path="${archive_path}.sha256"

echo "Downloading StatLite ${version} for ${os}/${arch}..."
curl -fsSL "$archive_url" -o "$archive_path"
curl -fsSL "$checksum_url" -o "$checksum_path"

expected_hash="$(awk '{print $1; exit}' "$checksum_path")"
[ -n "$expected_hash" ] || fail "checksum file is empty: ${checksum_url}"

if command -v sha256sum >/dev/null 2>&1; then
  actual_hash="$(sha256sum "$archive_path" | awk '{print $1; exit}')"
elif command -v shasum >/dev/null 2>&1; then
  actual_hash="$(shasum -a 256 "$archive_path" | awk '{print $1; exit}')"
else
  fail "missing required command: sha256sum or shasum"
fi

[ "$actual_hash" = "$expected_hash" ] || fail "checksum mismatch for ${archive_name}"

tar -xzf "$archive_path" -C "$tmp_dir" "$binary_name"
[ -f "${tmp_dir}/${binary_name}" ] || fail "archive did not contain ${binary_name}"

cp "${tmp_dir}/${binary_name}" "${install_dir}/${binary_name}"
chmod 0755 "${install_dir}/${binary_name}"

echo "Installed ${binary_name} to ${install_dir}/${binary_name}"

case ":${PATH:-}:" in
  *:"$install_dir":*) ;;
  *)
    echo "Add ${install_dir} to your PATH to run ${binary_name} from any directory."
    ;;
esac

"${install_dir}/${binary_name}" --version
