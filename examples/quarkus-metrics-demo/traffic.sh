#!/bin/sh
set -eu
base=${BASE_URL:-http://127.0.0.1:8080}
curl -fsS "$base/ok" >/dev/null
curl -sS -o /dev/null -w '%{http_code}\n' "$base/missing" | grep -qx 404
curl -sS -o /dev/null -w '%{http_code}\n' "$base/client-error" | grep -qx 418
curl -sS -o /dev/null -w '%{http_code}\n' "$base/server-error" | grep -qx 500
