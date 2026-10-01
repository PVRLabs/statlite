#!/bin/sh
set -eu
base=${BASE_URL:-http://127.0.0.1:18084}
expect_status() {
    actual=$(curl --noproxy '*' --max-time 5 -sS -o /dev/null -w '%{http_code}' "$base$2")
    [ "$actual" = "$1" ] || { printf 'expected %s for %s, got %s\n' "$1" "$2" "$actual" >&2; exit 1; }
}
expect_status 200 /probe/ok
expect_status 404 /probe/missing
expect_status 400 /probe/bad
expect_status 500 /probe/error
