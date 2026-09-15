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
settings. The workflow's `packages: write` permission does not by itself grant
an existing package access. If publication fails with `permission_denied:
write_package`, add that access and rerun all jobs in the same workflow run
while its lightweight release tag still points to the intended release commit.

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
`linux/arm64`, and checks the image manifest and `--version` output.

## 3. Verify GitHub and GHCR

Check the GitHub Release assets and inspect both image tags. The first public
pull can take a short time to work after the image is pushed.

```bash
gh release view "$RELEASE_VERSION" --repo PVRLabs/statlite
docker buildx imagetools inspect "ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}"
docker buildx imagetools inspect ghcr.io/pvrlabs/statlite:latest
docker pull "ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}"
docker run --rm "ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}" --version
```

Start the published image and verify that the server becomes ready and serves
its self-metrics endpoint and dashboard:

```bash
set -euo pipefail
image="ghcr.io/pvrlabs/statlite:${RELEASE_VERSION#v}"
container_id="$(docker run -d -p 127.0.0.1:19090:9090 "$image")"
trap 'docker rm -f "$container_id" >/dev/null 2>&1 || true' EXIT

for attempt in $(seq 1 30); do
  if curl -fsS http://127.0.0.1:19090/healthz >/dev/null; then
    break
  fi
  sleep 1
done

curl -fsS http://127.0.0.1:19090/healthz \
  | grep -F "\"version\":\"$RELEASE_VERSION\"" >/dev/null
curl -fsS http://127.0.0.1:19090/statlite/metrics \
  | grep -F '"schema":"statlite-metrics/v1"' >/dev/null
curl -fsS http://127.0.0.1:19090/ \
  | grep -F '<title>StatLite</title>' >/dev/null
docker rm -f "$container_id" >/dev/null
trap - EXIT
```

Confirm the release has all expected platform archives and checksum files, the
GHCR manifests include amd64 and arm64, and the container reports the intended
release version. The smoke check also confirms the image starts with the
release version, responds through `/healthz` and `/statlite/metrics`, and serves
the dashboard page. It uses the local Docker credentials; an anonymous pull
check is optional when GHCR package visibility needs separate verification.

## 4. Bump `main` to the Next Development Version

After the release and GHCR checks pass, update the checked-in StatLite version
on `main` to the next `-dev` version, for example `v0.4.3-dev`, then commit and
push the change. Verify its `test.yml` run succeeds.

## 5. Dispatch the Homebrew Updater

After the StatLite and GHCR release succeeds and `main` has its next development
version, manually dispatch the canonical
[`update-formula.yml`](https://github.com/PVRLabs/homebrew-tap/actions/workflows/update-formula.yml)
workflow in `PVRLabs/homebrew-tap`. Select the `statlite` formula and enter the
release version, including the `v` prefix, for example `v0.4.2`. From the tap
repository, dispatch and monitor it with:

```bash
gh workflow run update-formula.yml --repo PVRLabs/homebrew-tap --ref main \
  -f formula=statlite -f version="$RELEASE_VERSION"
gh run list --repo PVRLabs/homebrew-tap --workflow update-formula.yml --limit 5
```

Identify the new run, then confirm it succeeded and inspect the resulting
formula change on the tap's `main` branch:

```bash
export TAP_RUN_ID=123456789
gh run watch "$TAP_RUN_ID" --repo PVRLabs/homebrew-tap --exit-status
gh run view "$TAP_RUN_ID" --repo PVRLabs/homebrew-tap
```

The tap updater stays an independent manual operation. StatLite does not trigger
the tap workflow automatically.

## 6. Install Checks and Announcement

Install or upgrade StatLite with Homebrew, verify the installed version, and
run the formula checks:

```bash
brew update
brew audit --formula pvrlabs/tap/statlite
if brew list --formula statlite >/dev/null 2>&1; then
  brew upgrade pvrlabs/tap/statlite
else
  brew install pvrlabs/tap/statlite
fi
brew test pvrlabs/tap/statlite
statlite --version
```

Confirm the output is `statlite $RELEASE_VERSION`. Once the GitHub Release,
GHCR images, and tap formula are verified, announce the release.

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

Then complete the GitHub/GHCR verification above before dispatching the
Homebrew updater.
