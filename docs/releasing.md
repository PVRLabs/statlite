# Releasing StatLite

This document describes the public OSS release process for StatLite.

Release tags use exact versions, such as `vX.Y.Z`. The `main` branch keeps a
development version, such as `vX.Y.Z-dev`, so source builds are clearly
distinguishable from published release binaries.

## What Gets Released

The release workflow builds archives for:

- macOS `amd64`
- macOS `arm64`
- Linux `amd64`
- Linux `arm64`

Each artifact is a `.tar.gz` containing the `statlite` binary, standalone
third-party dashboard license notices, and a matching `.sha256` file. The
dashboard assets are embedded in the binary; container images do not include
separate license files. Windows artifacts are not part of the initial release.

Archive names use this pattern:

```text
statlite_X.Y.Z_darwin_amd64.tar.gz
statlite_X.Y.Z_darwin_arm64.tar.gz
statlite_X.Y.Z_linux_amd64.tar.gz
statlite_X.Y.Z_linux_arm64.tar.gz
```

The version component omits the leading `v` from the Git tag.

## Versioning

`internal/version.Version` is the default version for source builds. Release
builds override it from the Git tag with:

```bash
-ldflags="-s -w -X github.com/pvrlabs/statlite/internal/version.Version=${RELEASE_VERSION}"
```

That means a source build from `main` reports the checked-in `-dev` version,
while release archives report the exact tag:

```bash
statlite --version
```

`GET /healthz` exposes the same version string and SQLite readiness. It is not
a dashboard metrics endpoint.

## Release Journal

Keep a local process journal at `release/release-vX.Y.Z-journal.md` for each
release. The `release/` directory is ignored because the journal is an
operator's working record, not a release artifact. Start the journal before
the pre-release checks and record the commands, results, blockers, publication
verification, and post-release documentation work there.

## Before Releasing

1. Choose the release version:

```bash
RELEASE_VERSION=vX.Y.Z
```

2. Update `CHANGELOG.md` with the main user-facing changes for the release.
3. Confirm `internal/version/version.go` on `main` contains a `-dev` version.
4. Run the Go and dashboard JavaScript test suites:

```bash
go test ./...
node --test internal/dashboard/static/dashboard.test.js
```

5. Build a local release-style binary and confirm the version override:

```bash
go build -trimpath -ldflags="-s -w -X github.com/pvrlabs/statlite/internal/version.Version=${RELEASE_VERSION}" -o statlite ./cmd/statlite
./statlite --version
```

6. Review `.github/workflows/release.yml` if archive names, platforms, or the
   binary path changed.

## Operator Authentication

Run these commands as the release operator before pushing the tag or publishing
the container. Do not put tokens in commands, shell history, repository files,
or chat messages.

Authenticate GitHub CLI and configure Git to use it for the HTTPS remote:

```bash
gh auth login --hostname github.com --git-protocol https --web
gh auth status
ORIGIN_URL="$(git remote get-url origin)"
case "${ORIGIN_URL}" in
  https://*) gh auth setup-git ;;
  *) echo "origin is not HTTPS; skip gh auth setup-git" ;;
esac
```

Authenticate Docker to GHCR separately. The GitHub CLI login does not provide
Docker registry credentials. Use a GitHub classic personal access token with
`read:packages` and `write:packages`. Enter it through a hidden prompt:

```bash
read -s GHCR_TOKEN
printf '\n'
printf '%s' "$GHCR_TOKEN" | docker login ghcr.io \
  --username YOUR_GITHUB_USERNAME \
  --password-stdin
unset GHCR_TOKEN
```

If the organization requires SSO, authorize the token for the organization.
Before the first push, confirm the organization allows public package creation.
The package itself may not exist until the first authenticated push.

## Release Steps

1. Commit any release-readiness changes.
2. Create a Git tag that matches the release version:

```bash
git tag "${RELEASE_VERSION}"
```

3. Push the tag:

```bash
git push origin "${RELEASE_VERSION}"
```

4. Confirm the `release` workflow publishes all four archives and checksums to
   the GitHub Release. Find the run for this release tag, then monitor that run:

```bash
RUN_ID="$(gh run list \
  --workflow release.yml \
  --limit 20 \
  --json databaseId,headBranch,event,createdAt \
  --jq ".[] | select(.headBranch == \"${RELEASE_VERSION}\" and .event == \"push\") | .databaseId" \
  | head -n 1)"
test -n "${RUN_ID}" || { echo "release workflow run not found"; exit 1; }
gh run watch "${RUN_ID}" --exit-status
gh release view "${RELEASE_VERSION}" --json tagName,name,isDraft,isPrerelease,assets,url
```

5. Publish the versioned container image and `:latest` using the section below
   before changing the source version to the next development version.
6. After the release archives and container images are public, bump
   `internal/version/version.go` on `main` to the next development version, for
   example `v0.1.1-dev` after releasing `v0.1.0`.
7. Commit and push the `-dev` bump.
8. Update and publish the Homebrew tap as described below, then complete the
   installer and Homebrew verification for the release.

The workflow is triggered by pushes of `v*` tags.

## Publishing the Container Image

Container publication is currently manual. Publish the versioned image and
`:latest` from the same multi-platform build.

Confirm the active Buildx builder supports both target platforms:

```bash
docker buildx inspect --bootstrap
```

Authenticate Docker to `ghcr.io` without storing credentials in the repository
or shell history. This section must run before the post-release `-dev` bump.
Check out the release tag, with `RELEASE_VERSION` set as described above:

```bash
git switch --detach "${RELEASE_VERSION}"
```

Then run the build from the repository root:

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --build-arg VERSION="${RELEASE_VERSION}" \
  --tag "ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}" \
  --tag ghcr.io/pvrlabs/statlite:latest \
  --push \
  .
```

For example, `RELEASE_VERSION=v0.2.1` publishes these tags:

```text
ghcr.io/pvrlabs/statlite:0.2.1
ghcr.io/pvrlabs/statlite:latest
```

Both images report `statlite v0.2.1`.

After the initial authenticated push creates the package, confirm the GHCR
package visibility is public before testing anonymous pulls.

Inspect both manifests and confirm they contain `linux/amd64` and
`linux/arm64`:

```bash
docker buildx imagetools inspect \
  "ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}"
docker buildx imagetools inspect ghcr.io/pvrlabs/statlite:latest
```

Pull the versioned image and verify its version:

```bash
docker pull "ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}"
docker run --rm "ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}" --version
```

Run the published image and verify `/healthz`, `/statlite/metrics`, the
dashboard, and the `statlite-self` target. Then confirm the public package can
be pulled without GHCR credentials:

```bash
docker run --rm \
  -p 127.0.0.1:9090:9090 \
  "ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}"
```

After stopping that container, verify the public `:latest` image:

```bash
ANON_DOCKER_HOST="$(docker context inspect "$(docker context show)" \
  --format '{{ (index .Endpoints "docker").Host }}')"

(
  ANON_DOCKER_CONFIG="$(mktemp -d)"
  trap 'rm -rf "${ANON_DOCKER_CONFIG}"' EXIT
  DOCKER_CONFIG="${ANON_DOCKER_CONFIG}" \
    DOCKER_HOST="${ANON_DOCKER_HOST}" \
    docker pull ghcr.io/pvrlabs/statlite:latest
)
```

Run the anonymously pulled image and repeat the health, metrics, dashboard, and
self-monitoring checks. The temporary Docker configuration leaves the
maintainer's normal Docker credentials unchanged and keeps the active Docker
daemon endpoint for local Unix-socket contexts such as Colima.

After container verification, return to `main` before performing the post-release
`-dev` version bump:

```bash
git switch main
```

## Verification Checklist

- The GitHub Release page exists for the new tag.
- The release has four `.tar.gz` assets and four `.tar.gz.sha256` assets.
- The release tag contains `scripts/systemd.sh`, and the tagged raw GitHub URL
  is accessible.
- Each archive contains a single `statlite` binary.
- `statlite --version` reports the release tag.
- `/healthz` reports the same version and SQLite readiness.
- Source builds from `main` after the release report the next `-dev` version.
- `go test ./...` passes.
- `go build -o statlite ./cmd/statlite` works for source users.
- README Quick Start still works from a clean clone.
- The versioned container manifest contains `linux/amd64` and `linux/arm64`.
- The `:latest` container manifest contains `linux/amd64` and `linux/arm64`.
- The versioned container reports `statlite ${RELEASE_VERSION}` from `--version`.
- An anonymous pull of `:latest` succeeds, and the image passes health, metrics,
  dashboard, and self-monitoring checks.

## Publishing the Homebrew Tap

The Homebrew formula is maintained in the separate `PVRLabs/homebrew-tap`
repository. Update that repository independently after the GitHub Release assets
are available.

From the tap checkout, update `Formula/statlite.rb`:

- Set `version` to `${RELEASE_VERSION#v}`.
- Update the four `sha256` values from the matching GitHub Release archives.

Then validate and publish the formula:

```bash
brew audit --formula Formula/statlite.rb
brew test Formula/statlite.rb
git diff --check
git add Formula/statlite.rb
git commit -m "statlite: update to ${RELEASE_VERSION#v}"
git push origin main
```

Verify installation through the tap and confirm the binary reports the release
version:

```bash
brew update
brew upgrade pvrlabs/tap/statlite
statlite --version
```

The output should be `statlite ${RELEASE_VERSION}`.

## Manual Fallback

If the release workflow is unavailable, build the archives locally with the same
platform matrix, archive names, and `-ldflags` version override used by
`.github/workflows/release.yml`, then upload the archives and checksum files to
the GitHub Release manually.
