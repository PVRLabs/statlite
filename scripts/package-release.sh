#!/usr/bin/env bash
# Run from the repository root. Builds one release archive and its checksum.
set -eu

: "${GOOS:?set GOOS}"
: "${GOARCH:?set GOARCH}"
: "${RELEASE_TAG:?set RELEASE_TAG}"
export CGO_ENABLED=0

binary_name=statlite
extension=tar.gz
if [ "$GOOS" = windows ]; then
  binary_name=statlite.exe
  extension=zip
fi
out_dir="dist/statlite_${GOOS}_${GOARCH}"
archive_name="statlite_${RELEASE_TAG#v}_${GOOS}_${GOARCH}.${extension}"
mkdir -p "$out_dir"
go build -trimpath -ldflags="-s -w -X github.com/pvrlabs/statlite/internal/version.Version=${RELEASE_TAG}" \
  -o "${out_dir}/${binary_name}" ./cmd/statlite
cp internal/dashboard/static/vendor/CHARTJS-LICENSE.md "${out_dir}/LICENSE-Chart.js.md"
cp internal/dashboard/static/fonts/ORBITRON-LICENSE.txt "${out_dir}/LICENSE-Orbitron.txt"

if [ "$GOOS" = windows ]; then
  cp docs/windows-readme.txt "${out_dir}/readme.txt"
  cp statlite.yaml "${out_dir}/statlite.yaml"
  # Write a fresh archive with an explicit member list, including on retries.
  python3 - "$out_dir" "dist/$archive_name" <<'PY'
import sys
from pathlib import Path
from zipfile import ZIP_DEFLATED, ZipFile

source = Path(sys.argv[1])
with ZipFile(sys.argv[2], "w", compression=ZIP_DEFLATED) as archive:
    for name in ("statlite.exe", "readme.txt", "statlite.yaml",
                 "LICENSE-Chart.js.md", "LICENSE-Orbitron.txt"):
        archive.write(source / name, name)
PY
else
  tar -czf "dist/$archive_name" -C "$out_dir" "$binary_name" LICENSE-Chart.js.md LICENSE-Orbitron.txt
fi
(cd dist && shasum -a 256 "$archive_name" > "$archive_name.sha256")
