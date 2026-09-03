#!/usr/bin/env bash
set -eu

NEW_TAG="${1:-}"
if [ -z "$NEW_TAG" ]; then
  echo "Usage: $0 <new-tag>" >&2
  exit 1
fi

if ! echo "$NEW_TAG" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "Invalid release tag: $NEW_TAG (expected vX.Y.Z)" >&2
  exit 1
fi

PREV_TAG=$(git tag --sort=-version:refname | grep -v '\-dev' | awk -v t="$NEW_TAG" 'found{print;exit} $0==t{found=1}')
if [ -z "$PREV_TAG" ]; then
  PREV_TAG=$(git tag --sort=-version:refname | grep -v '\-dev' | head -1)
fi

if git rev-parse -q --verify "refs/tags/${NEW_TAG}" >/dev/null; then
  END_REF="$NEW_TAG"
else
  END_REF="HEAD"
fi

if [ -n "$PREV_TAG" ]; then
  COMMIT_RANGE="${PREV_TAG}..${END_REF}"
else
  COMMIT_RANGE="$END_REF"
fi

COMMITS=$(git log "$COMMIT_RANGE" --oneline --no-decorate | grep -viE '^[0-9a-f]+ (release:|bump version to|bump to|bump version:|chore: bump( main)? to .*dev)' || true)
COMMIT_COUNT=$(echo "$COMMITS" | grep -c . || true)

echo "## What's Changed"
echo

if [ "$COMMIT_COUNT" -eq 0 ]; then
  if [ -n "$PREV_TAG" ]; then
    echo "No changes between $PREV_TAG and $NEW_TAG."
  else
    echo "No changes found for $NEW_TAG."
  fi
  echo
  if [ -n "$PREV_TAG" ]; then
    echo "**Full Changelog**: https://github.com/PVRLabs/statlite/compare/${PREV_TAG}...${NEW_TAG}"
  fi
  echo "**Release Changelog**: https://github.com/PVRLabs/statlite/blob/${NEW_TAG}/CHANGELOG.md"
  exit 0
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

OTHER="$WORK/other"

while IFS= read -r line; do
  commit_msg=$(echo "$line" | sed 's/^[0-9a-f]\{7,40\} //')
  issue=$(echo "$commit_msg" | sed -nE 's/.*(#[0-9]+).*/\1/p' | tail -1)
  desc=$(echo "$commit_msg" | sed -E 's/[[:space:]]*\([^)]*#[0-9]+\)[[:space:]]*$//' | sed -E 's/[[:space:]]*[(]?#[0-9]+[)]?[[:space:]]*$//' | sed -E 's/^[[:space:]]*#[0-9]+[): -]*//' | sed 's/^[[:space:]]*//')

  if [ -n "$issue" ]; then
    echo "$desc" >> "$WORK/$issue"
  else
    echo "$desc" >> "$OTHER"
  fi
done <<< "$COMMITS"

for f in "$WORK"/#*; do
  [ -f "$f" ] || continue
  num=$(echo "$(basename "$f")" | sed 's/^#//')
  echo "$num $f"
done | sort -n | while IFS=' ' read -r num path; do
  issue="#$num"
  first=$(head -1 "$path")
  title=$(echo "$first" | sed -E 's/^(feat|fix|docs|chore|refactor|test|ci|perf|style|build|revert)(\([^)]+\))?:\s*//i' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
  echo "**$issue — $title**"
  while IFS= read -r item; do
    echo "* $item"
  done < "$path"
  echo
done

if [ -s "$OTHER" ]; then
  echo "**Other**"
  while IFS= read -r item; do
    echo "* $item"
  done < "$OTHER"
  echo
fi

if [ -n "$PREV_TAG" ]; then
  echo "**Full Changelog**: https://github.com/PVRLabs/statlite/compare/${PREV_TAG}...${NEW_TAG}"
fi

echo "**Release Changelog**: https://github.com/PVRLabs/statlite/blob/${NEW_TAG}/CHANGELOG.md"
