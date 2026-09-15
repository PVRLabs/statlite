#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf 'Usage: %s <homebrew-tap-run-id> <release-version-vX.Y.Z>\n' "$0" >&2
  exit 2
}

fail() {
  printf 'Homebrew release verification failed: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 2 ]] || usage

TAP_RUN_ID=$1
RELEASE_VERSION=$2

[[ "$TAP_RUN_ID" =~ ^[0-9]+$ ]] || usage
[[ "$RELEASE_VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || usage

command -v gh >/dev/null 2>&1 || fail "GitHub CLI (gh) is required"
command -v brew >/dev/null 2>&1 || fail "Homebrew (brew) is required"

printf 'Waiting for Homebrew tap updater run %s to succeed...\n' "$TAP_RUN_ID"
gh run watch "$TAP_RUN_ID" --repo PVRLabs/homebrew-tap --exit-status

printf 'Updating Homebrew metadata...\n'
brew update

printf 'Auditing pvrlabs/tap/statlite...\n'
brew audit --formula pvrlabs/tap/statlite

if brew list --formula statlite >/dev/null 2>&1; then
  printf 'Upgrading pvrlabs/tap/statlite...\n'
  brew upgrade pvrlabs/tap/statlite
else
  printf 'Installing pvrlabs/tap/statlite...\n'
  brew install pvrlabs/tap/statlite
fi

printf 'Testing pvrlabs/tap/statlite...\n'
brew test pvrlabs/tap/statlite

command -v statlite >/dev/null 2>&1 || fail "statlite was not found on PATH after install or upgrade"
expected_version="statlite $RELEASE_VERSION"
actual_version=$(statlite --version)
if [[ "$actual_version" != "$expected_version" ]]; then
  fail "expected '$expected_version', got '$actual_version'"
fi

printf 'Verified %s\n' "$actual_version"
