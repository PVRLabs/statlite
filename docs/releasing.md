# Releasing StatLite

StatLite releases use a semi-automated maintainer workflow. The GitHub Actions
release workflow creates the tag, builds and verifies the release archives,
publishes the GitHub Release, and publishes multi-platform GHCR images. The
Homebrew tap update remains a separate manual dispatch after those checks pass.

The maintainer flow is:

`prepare release commit → push and verify CI → dispatch StatLite release → verify GitHub/GHCR → bump main to next -dev → dispatch Homebrew updater → install checks → announcement`

## What Gets Released

For release `vX.Y.Z`, the release workflow produces archives for Linux and macOS
on amd64 and arm64, plus SHA-256 checksum files. It publishes GHCR images tagged
`X.Y.Z` and `latest` from the same multi-platform build. The image embeds the
release version, which is returned by `statlite --version`.

The workflow is started manually from `main` with an explicit `vX.Y.Z` input.
It checks that the dispatch is from `main`, the version format is valid, the
checked-in StatLite version matches, and the changelog contains the release. It
creates the tag at the dispatched commit. It does not update the Homebrew tap.

The manual `:dev` image workflow remains separate. See [Docker development
images](docker.md) for its process.

Before the first release workflow publication, grant the `PVRLabs/statlite`
repository **Write** access under the GHCR package's **Manage Actions access**
settings; this is normally a one-time setup. The workflow's `packages: write`
permission does not by itself grant an existing package access. If publication
fails with `permission_denied: write_package`, add that access and rerun all
jobs in the same workflow run while its lightweight release tag still points
to the intended release commit.

## Before Releasing

Prepare a release commit on `main`:

1. Set `RELEASE_VERSION` below to the intended version, including the `v`
   prefix, for example `v0.4.2`.
2. Update the changelog with a heading for that exact version.
3. Update `internal/version/version.go` to that exact version, including the
   `v` prefix.
4. Review the changes and run the relevant Go and web checks, then build StatLite
   locally.
5. Commit the release preparation changes.

```bash
export RELEASE_VERSION=v0.4.2
```

## 1. Push the Release Commit and Verify CI

Push the release commit to `main` and confirm the `test.yml` workflow succeeds
for that commit before publishing.

```bash
git push origin main
gh run list --repo PVRLabs/statlite --workflow test.yml --branch main --limit 5
```

Find the successful `test.yml` run for the pushed commit in the list, then set
its ID and watch it finish:

```bash
export CI_RUN_ID=123456789
gh run watch "$CI_RUN_ID" --repo PVRLabs/statlite --exit-status
```

Dispatch the release from `main` only after CI for the pushed release commit
has passed.

## 2. Dispatch the StatLite Release

Authenticate with GitHub CLI, then start `release.yml` with the explicit
version. This creates the tag at the dispatched commit and publishes the
GitHub Release and GHCR images.

```bash
gh auth status
gh run list --repo PVRLabs/statlite --workflow release.yml --limit 5
gh workflow run release.yml --repo PVRLabs/statlite --ref main \
  -f version="$RELEASE_VERSION"
gh run list --repo PVRLabs/statlite --workflow release.yml --limit 5
```

Identify the new dispatch run, set its ID, and watch it finish:

```bash
export RELEASE_RUN_ID=123456789
gh run watch "$RELEASE_RUN_ID" --repo PVRLabs/statlite --exit-status
gh run view "$RELEASE_RUN_ID" --repo PVRLabs/statlite
```

The run should be a `workflow_dispatch` from `main` with the requested version.
The workflow validates the release invariants, creates the tag, reuses the
existing archive build and release-note generation, checks the expected release
assets and checksums, publishes the two GHCR tags for `linux/amd64` and
`linux/arm64`, checks both image manifests and `--version` output, then pulls
the versioned image and smoke-tests readiness, self-metrics schema, and the
dashboard response. The smoke-test container is always removed.

Release notes use the matching version section from `CHANGELOG.md`, followed by
the full comparison link. Keep that section focused on user-facing changes.
When the section is missing, the release script falls back to commit history.

## 3. Verify the GitHub Release

Manually confirm the GitHub Release contains the expected archives and
checksum files. The successful `release.yml` run is authoritative for both
GHCR manifests, image versions, startup, and endpoint checks, so the normal
release flow does not require a local Docker or Buildx setup.

```bash
gh release view "$RELEASE_VERSION" --repo PVRLabs/statlite
```

For optional GHCR troubleshooting, inspect the published manifests manually:

```bash
docker buildx imagetools inspect "ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}"
docker buildx imagetools inspect ghcr.io/pvrlabs/statlite:latest
```

An anonymous pull check is also optional when GHCR package visibility needs
separate verification.

## 4. Bump `main` to the Next Development Version

After the release and GHCR checks pass, update the checked-in StatLite version
on `main` to the next `-dev` version, for example `v0.4.3-dev`, then commit and
push the change. Verify its `test.yml` run succeeds.

## 5. Dispatch the Homebrew Updater and Verify Installation

After the StatLite and GHCR release succeeds and `main` has its next development
version, manually dispatch the canonical
[`update-formula.yml`](https://github.com/PVRLabs/homebrew-tap/actions/workflows/update-formula.yml)
workflow in `PVRLabs/homebrew-tap`. Select the `statlite` formula and enter the
release version, including the `v` prefix, for example `v0.4.2`. From the
StatLite checkout, dispatch it and identify the new run:

```bash
gh workflow run update-formula.yml --repo PVRLabs/homebrew-tap --ref main \
  -f formula=statlite -f version="$RELEASE_VERSION"
gh run list --repo PVRLabs/homebrew-tap --workflow update-formula.yml --limit 5
```

Identify the new run, then run the local verification script. It waits for the
tap updater to finish successfully before touching the local Homebrew install,
then audits, installs or upgrades, tests the formula, and confirms its version.

```bash
export TAP_RUN_ID=123456789
scripts/verify-homebrew-release.sh "$TAP_RUN_ID" "$RELEASE_VERSION"
gh run view "$TAP_RUN_ID" --repo PVRLabs/homebrew-tap
```

The tap updater stays an independent manual operation. StatLite does not trigger
the tap workflow automatically.

## 6. Announcement

Once the GitHub Release, GHCR images, and tap formula are verified, announce
the release in the repository's [Announcements discussion
category](https://github.com/PVRLabs/statlite/discussions/categories/announcements).
Use the release changelog, tagged documentation, and published artifacts as the
source of truth. Follow this structure:

- Title: `StatLite X.Y.Z: <two concise release themes>`.
- A short opening paragraph stating what the release adds and who benefits.
- `## What is new in X.Y.Z`, with `###` headings for the main user-facing
  changes and concrete behavior or configuration details.
- `## Try it`, with commands for the exact versioned container and relevant
  install or update paths that have been verified.
- `## Learn more`, linking the exact GitHub Release, tagged documentation, and
  changelog, followed by a brief invitation for feedback or bug reports.

Include certification results only when they were completed for this release.
Use the exact release tag in links and examples so the announcement remains
accurate after newer versions are published. The [v0.4.1
announcement](https://github.com/PVRLabs/statlite/discussions/15) is an example.

## Recovery

Publication can leave durable results before a later step fails: the tag can
exist before the archive build finishes, the GitHub Release can exist before
GHCR publication succeeds, and `latest` may have been updated before container
verification fails.

For an ordinary transient failure, inspect the failed Actions run and its tag.
If the workflow-created lightweight tag still points to the release commit,
rerun that workflow run with **Re-run all jobs**. The release workflow accepts
that matching tag and can replace the release assets while publication is
retried.

If the tag is annotated or points to a different commit, stop and inspect it
manually before taking further release action. Do not try to repair or delete
release state as part of the routine retry path.

## Manual Fallback

Use this only when the release workflow cannot be used. If a tag or partial
release already exists, inspect that state first. Set `RELEASE_VERSION` and
confirm the current `main` commit has the matching checked-in version and
changelog entry. If the release tag does not exist, create it at that prepared
commit:

```bash
export RELEASE_VERSION=v0.4.2
export PLAIN_VERSION="${RELEASE_VERSION#v}"
export RELEASE_SHA="$(git rev-parse HEAD)"
git tag "$RELEASE_VERSION" "$RELEASE_SHA"
git push origin "refs/tags/$RELEASE_VERSION"
```

Build the same four archives and portable checksums locally:

```bash
mkdir -p dist
for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  out_dir="dist/statlite_${GOOS}_${GOARCH}"
  archive="statlite_${PLAIN_VERSION}_${GOOS}_${GOARCH}.tar.gz"
  mkdir -p "$out_dir"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath \
    -ldflags="-s -w -X github.com/pvrlabs/statlite/internal/version.Version=$RELEASE_VERSION" \
    -o "$out_dir/statlite" ./cmd/statlite
  cp internal/dashboard/static/vendor/CHARTJS-LICENSE.md "$out_dir/LICENSE-Chart.js.md"
  cp internal/dashboard/static/fonts/ORBITRON-LICENSE.txt "$out_dir/LICENSE-Orbitron.txt"
  tar -czf "dist/$archive" -C "$out_dir" statlite LICENSE-Chart.js.md LICENSE-Orbitron.txt
  (cd dist && shasum -a 256 "$archive" > "$archive.sha256")
done
bash scripts/generate-release-notes.sh "$RELEASE_VERSION" > dist/release-notes.md
gh release create "$RELEASE_VERSION" --repo PVRLabs/statlite \
  --verify-tag --title "StatLite $RELEASE_VERSION" \
  --notes-file dist/release-notes.md dist/*.tar.gz dist/*.tar.gz.sha256
```

For GHCR, authenticate with a token that can publish packages, then publish
both tags from the same multi-platform build:

```bash
read -s GHCR_TOKEN
printf '\n'
printf '%s' "$GHCR_TOKEN" | docker login ghcr.io \
  --username YOUR_GITHUB_USERNAME --password-stdin
unset GHCR_TOKEN
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg VERSION="$RELEASE_VERSION" \
  --tag "ghcr.io/pvrlabs/statlite:$PLAIN_VERSION" \
  --tag ghcr.io/pvrlabs/statlite:latest --push .
```

The manual fallback bypasses the workflow smoke check, so run this lightweight
runtime verification before dispatching the Homebrew updater:

```bash
set -euo pipefail
image="ghcr.io/pvrlabs/statlite:$PLAIN_VERSION"
container_id=
cleanup_container() {
  if [ -n "$container_id" ]; then
    docker rm -f "$container_id" >/dev/null 2>&1 || true
  fi
}
trap cleanup_container EXIT

docker run --pull=always --rm "$image" --version \
  | grep -F "statlite $RELEASE_VERSION" >/dev/null
container_id="$(docker run --pull=always -d \
  -p 127.0.0.1:19090:9090 "$image")"

ready=false
for attempt in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:19090/healthz >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 1
done
if [ "$ready" != true ]; then
  echo 'StatLite container did not become ready at /healthz' >&2
  docker logs "$container_id" >&2 || true
  exit 1
fi

curl -fsS http://127.0.0.1:19090/healthz \
  | grep -F "\"version\":\"$RELEASE_VERSION\"" >/dev/null
curl -fsS http://127.0.0.1:19090/statlite/metrics \
  | grep -F '"schema":"statlite-metrics/v1"' >/dev/null
curl -fsS http://127.0.0.1:19090/ \
  | grep -F '<title>StatLite</title>' >/dev/null
```

Before dispatching the Homebrew updater, verify the GitHub Release and both
published GHCR manifests as required fallback checks:

```bash
gh release view "$RELEASE_VERSION" --repo PVRLabs/statlite
docker buildx imagetools inspect "ghcr.io/pvrlabs/statlite:$PLAIN_VERSION"
docker buildx imagetools inspect ghcr.io/pvrlabs/statlite:latest
```
