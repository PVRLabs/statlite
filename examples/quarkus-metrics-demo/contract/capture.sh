#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
java_cmd=${JAVA_HOME:+$JAVA_HOME/bin/}java
port=${PORT:-18080}
management_port=${MANAGEMENT_PORT:-19000}
base=http://127.0.0.1:$port
metrics=http://127.0.0.1:$management_port/q/metrics
work=${TMPDIR:-/tmp}/statlite-quarkus-capture-$$
mkdir "$work"
pid=
cleanup() {
  if test -n "$pid"; then
    kill "$pid" 2>/dev/null || true
  fi
  rm -rf "$work"
}
trap cleanup EXIT INT TERM

start() {
  "$java_cmd" -Dquarkus.http.port="$port" -Dquarkus.management.port="$management_port" -jar "$root/target/quarkus-app/quarkus-run.jar" >"$work/app-$1.log" 2>&1 &
  pid=$!
  for _ in $(seq 1 60); do
    curl -fsS -H 'Accept: text/plain' "$metrics" >"$work/$1.prom" 2>/dev/null && return
    sleep 1
  done
  return 1
}

require_finite_family() {
  awk -v family="$1" '
    index($1, family) == 1 && $2 !~ /^(NaN|[+-]?Inf)$/ { found = 1 }
    END { exit !found }
  ' "$2"
}

(cd "$root" && mvn package -DskipTests -Dquarkus.analytics.disabled=true)
start before
grep -q '^process_start_time_seconds ' "$work/before.prom"
grep -q '^process_uptime_seconds ' "$work/before.prom"
grep -q '^process_cpu_usage ' "$work/before.prom"
grep -q '^jvm_memory_used_bytes{.*area="heap".*} ' "$work/before.prom"
require_finite_family process_start_time_seconds "$work/before.prom"
require_finite_family process_uptime_seconds "$work/before.prom"
require_finite_family process_cpu_usage "$work/before.prom"
require_finite_family jvm_memory_used_bytes "$work/before.prom"
if grep -q '^http_server_requests_seconds_count' "$work/before.prom"; then
  printf 'unexpected HTTP request series before traffic\n' >&2
  exit 1
fi
BASE_URL="$base" "$root/traffic.sh"
curl -fsS -H 'Accept: text/plain' "$metrics" >"$work/after-traffic.prom"
grep -q '^http_server_requests_seconds_count{.*method="GET".*outcome="SUCCESS".*status="200".*} 1\.0$' "$work/after-traffic.prom"
grep -q '^http_server_requests_seconds_count{.*method="GET".*outcome="CLIENT_ERROR".*status="404".*} 1\.0$' "$work/after-traffic.prom"
grep -q '^http_server_requests_seconds_count{.*method="GET".*outcome="CLIENT_ERROR".*status="418".*} 1\.0$' "$work/after-traffic.prom"
grep -q '^http_server_requests_seconds_count{.*method="GET".*outcome="SERVER_ERROR".*status="500".*} 1\.0$' "$work/after-traffic.prom"
grep -q '^http_server_requests_seconds_sum{.*method="GET".*outcome="SUCCESS".*status="200".*} ' "$work/after-traffic.prom"
grep -q '^http_server_requests_seconds_sum{.*method="GET".*outcome="CLIENT_ERROR".*status="404".*} ' "$work/after-traffic.prom"
grep -q '^http_server_requests_seconds_sum{.*method="GET".*outcome="CLIENT_ERROR".*status="418".*} ' "$work/after-traffic.prom"
grep -q '^http_server_requests_seconds_sum{.*method="GET".*outcome="SERVER_ERROR".*status="500".*} ' "$work/after-traffic.prom"
require_finite_family http_server_requests_seconds_sum "$work/after-traffic.prom"
old=$(awk '$1 == "process_start_time_seconds" { print $2; exit }' "$work/before.prom")
kill "$pid"; wait "$pid" 2>/dev/null || true
start after-restart
new=$(awk '$1 == "process_start_time_seconds" { print $2; exit }' "$work/after-restart.prom")
test -n "$old" && test -n "$new" && test "$old" != "$new"
printf 'validated captures: %s\n' "$work"
