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

Each artifact is a `.tar.gz` containing the `statlite` binary and a matching
`.sha256` file. Windows artifacts are not part of the initial release.

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

`GET /healthz` exposes the same version string.

## Before Releasing

1. Choose the release version:

```bash
RELEASE_VERSION=vX.Y.Z
```

2. Confirm `internal/version/version.go` on `main` contains a `-dev` version.
3. Run the test suite:

```bash
go test ./...
```

4. Build a local release-style binary and confirm the version override:

```bash
go build -trimpath -ldflags="-s -w -X github.com/pvrlabs/statlite/internal/version.Version=${RELEASE_VERSION}" -o statlite ./cmd/statlite
./statlite --version
```

5. Review `.github/workflows/release.yml` if archive names, platforms, or the
   binary path changed.

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
   the GitHub Release.
5. After the release is public, bump `internal/version/version.go` on `main` to
   the next development version, for example `v0.1.1-dev` after releasing
   `v0.1.0`.
6. Commit and push the `-dev` bump.
7. Continue with installer and Homebrew verification for the release.

The workflow is triggered by pushes of `v*` tags.

## Verification Checklist

- The GitHub Release page exists for the new tag.
- The release has four `.tar.gz` assets and four `.tar.gz.sha256` assets.
- Each archive contains a single `statlite` binary.
- `statlite --version` reports the release tag.
- `/healthz` reports the same version.
- Source builds from `main` after the release report the next `-dev` version.
- `go test ./...` passes.
- `go build -o statlite ./cmd/statlite` works for source users.
- README Quick Start still works from a clean clone.

## Manual Fallback

If the release workflow is unavailable, build the archives locally with the same
platform matrix, archive names, and `-ldflags` version override used by
`.github/workflows/release.yml`, then upload the archives and checksum files to
the GitHub Release manually.
