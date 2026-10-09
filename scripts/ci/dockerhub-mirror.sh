#!/usr/bin/env bash
# Mirror an existing release. Docker Hub login and QEMU are supplied by Actions.
set -euo pipefail

: "${RELEASE_VERSION:?RELEASE_VERSION is required}"
: "${UPDATE_LATEST:?UPDATE_LATEST is required}"
[[ "$RELEASE_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  echo 'version must match vX.Y.Z' >&2
  exit 1
}
case "$UPDATE_LATEST" in
  true|false) ;;
  *) echo 'UPDATE_LATEST must be true or false' >&2; exit 1 ;;
esac

source_image=ghcr.io/pvrlabs/statlite
target_image=docker.io/pvrlabs/statlite
tag="${RELEASE_VERSION#v}"
work_dir="$(mktemp -d)"
container_id=
cleanup() {
  if [ -n "$container_id" ]; then
    docker rm --force "$container_id" >/dev/null 2>&1 || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

# Pin the source index so all subsequent copies and checks use this release.
source_digest="$(docker buildx imagetools inspect "$source_image:$tag" --format '{{.Manifest.Digest}}')"
[[ "$source_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || {
  echo 'Could not resolve GHCR release digest' >&2
  exit 1
}
source_ref="$source_image@$source_digest"

# Compare platform config and layer content, allowing registry manifest
# serialization/media types to differ. Require exactly the supported platforms.
identity() {
  local image="$1" manifest digest arch repository
  repository="${image%@*}"
  repository="${repository%:*}"
  manifest="$(docker buildx imagetools inspect --raw "$image")"
  if [ "$(jq -r '.manifests[]?.platform | select(.os == "linux") | "\(.os)/\(.architecture)"' <<< "$manifest" | sort)" != "$(printf 'linux/amd64\nlinux/arm64')" ]; then
    echo "$image must contain linux/amd64 and linux/arm64 exactly once" >&2
    return 1
  fi
  for arch in amd64 arm64; do
    digest="$(jq -r --arg arch "$arch" '.manifests[] | select(.platform.os == "linux" and .platform.architecture == $arch) | .digest' <<< "$manifest")"
    docker buildx imagetools inspect --raw "$repository@$digest" \
      | jq -ceS --arg arch "$arch" '{architecture: $arch, config: .config.digest, layers: [.layers[].digest]}'
  done
}

identity "$source_ref" > "$work_dir/source.identity"
require_current_latest() {
  identity "$source_image:latest" > "$work_dir/latest.identity"
  if ! cmp -s "$work_dir/source.identity" "$work_dir/latest.identity"; then
    echo "$RELEASE_VERSION does not match GHCR latest; refusing to update Docker Hub latest" >&2
    exit 1
  fi
}

if [ "$UPDATE_LATEST" = true ]; then
  require_current_latest
fi
docker buildx imagetools create --tag "$target_image:$tag" "$source_ref"

# Verify with an empty client credential store: all registry reads and pulls
# below must work anonymously, including on a fresh installation.
mkdir "$work_dir/anonymous"
verify_tag() (
  export DOCKER_CONFIG="$work_dir/anonymous"
  local image="$target_image:$1" arch actual_version
  identity "$image" > "$work_dir/target.identity"
  if ! cmp -s "$work_dir/source.identity" "$work_dir/target.identity"; then
    echo "$image platform content differs from the GHCR release" >&2
    exit 1
  fi
  for arch in amd64 arm64; do
    docker pull --platform "linux/$arch" "$image"
    actual_version="$(docker run --pull=always --rm --platform "linux/$arch" "$image" --version)"
    if [ "$actual_version" != "statlite $RELEASE_VERSION" ]; then
      echo "$image ($arch) reports '$actual_version', expected 'statlite $RELEASE_VERSION'" >&2
      exit 1
    fi
  done
)
verify_tag "$tag"

# Start the publicly pulled Docker Hub image and check its release identity.
container_id="$(DOCKER_CONFIG="$work_dir/anonymous" docker run --pull=always --detach \
  --platform linux/amd64 --publish 127.0.0.1::9090 "$target_image:$tag")"
port_mapping="$(docker port "$container_id" 9090/tcp)"
host_port="${port_mapping##*:}"
[[ "$host_port" =~ ^[0-9]+$ ]] || { echo "Invalid port mapping: $port_mapping" >&2; exit 1; }
base_url="http://127.0.0.1:$host_port"
for attempt in $(seq 1 30); do
  if curl -fsS --connect-timeout 1 --max-time 2 "$base_url/healthz" > "$work_dir/health.json"; then
    break
  fi
  if [ "$attempt" -eq 30 ]; then
    docker logs "$container_id" >&2 || true
    echo 'Docker Hub container did not become healthy' >&2
    exit 1
  fi
  sleep 1
done
jq -e --arg version "$RELEASE_VERSION" '.version == $version' "$work_dir/health.json" >/dev/null

# Promote only after versioned image verification, rechecking GHCR latest
# immediately before writing. Mirror runs are serialized by the workflow.
if [ "$UPDATE_LATEST" = true ]; then
  require_current_latest
  docker buildx imagetools create --tag "$target_image:latest" "$source_ref"
  verify_tag latest
fi
echo "Docker Hub mirror verified for $RELEASE_VERSION (latest=$UPDATE_LATEST)"
