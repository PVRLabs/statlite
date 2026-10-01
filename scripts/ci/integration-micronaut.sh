#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH=; cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH=; cd -- "$SCRIPT_DIR/../.." && pwd)
MICRONAUT_DIR="$REPO_DIR/examples/micronaut-metrics-demo"
STATLITE_BIN=${STATLITE_BIN:-"$REPO_DIR/statlite"}

WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/statlite-micronaut.XXXXXX")
APP_LOG="$WORK_DIR/micronaut.log"
STATLITE_LOG="$WORK_DIR/statlite.log"
STATLITE_CONFIG="$WORK_DIR/statlite.yaml"
APP_PID=
STATLITE_PID=

cleanup() {
	status=${1:-$?}
	trap - EXIT HUP INT TERM
	set +e
	stop_process() {
		name=$1
		pid=$2
		[ -n "$pid" ] || return
		if kill -0 "$pid" 2>/dev/null; then
			kill "$pid" 2>/dev/null || true
			i=0
			while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 25 ]; do
				sleep 0.2
				i=$((i + 1))
			done
			if kill -0 "$pid" 2>/dev/null; then
				printf 'forcing %s process %s to exit\n' "$name" "$pid" >&2
				kill -KILL "$pid" 2>/dev/null || true
			fi
		fi
		wait "$pid" 2>/dev/null || true
	}
	stop_process StatLite "$STATLITE_PID"
	stop_process Micronaut "$APP_PID"
	for owned_port in ${APP_PID:+18084} ${STATLITE_PID:+19094}; do
		if lsof -nP -iTCP:"$owned_port" -sTCP:LISTEN >/dev/null 2>&1; then
			printf 'listener remains on port %s after cleanup\n' "$owned_port" >&2
			status=1
		fi
	done
	if [ "$status" -ne 0 ]; then
		printf '%s\n' '--- Micronaut application log ---' >&2
		tail -100 "$APP_LOG" >&2 || true
		printf '%s\n' '--- StatLite log ---' >&2
		tail -100 "$STATLITE_LOG" >&2 || true
	fi
	rm -rf "$WORK_DIR"
	exit "$status"
}

trap 'cleanup 129' HUP
trap 'cleanup 130' INT
trap 'cleanup 143' TERM
trap cleanup EXIT

fail() {
	printf 'Micronaut integration failed: %s\n' "$*" >&2
	exit 1
}

[ -x "$STATLITE_BIN" ] || fail "StatLite binary is not executable: $STATLITE_BIN"

# Dedicated loopback ports; each workflow case runs in its own runner.
APP_URL=http://127.0.0.1:18084
METRICS_URL="$APP_URL/prometheus"
STATLITE_URL=http://127.0.0.1:19094

# Fail before starting if a listener would make readiness ambiguous.
for port in 18084 19094; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
        fail "port $port is already in use"
    fi
done
classpath=$(cat "$MICRONAUT_DIR/target/classpath.txt")
java -cp "$MICRONAUT_DIR/target/classes:$classpath" example.Application >"$APP_LOG" 2>&1 &
APP_PID=$!
wait_for_url() {
	name=$1
	url=$2
	pid=$3
	i=0
	while [ "$i" -lt 60 ]; do
		if curl --noproxy '*' --max-time 2 -fsS "$url" >/dev/null 2>&1; then
			return 0
		fi
		if ! kill -0 "$pid" 2>/dev/null; then
			fail "$name exited before becoming ready"
		fi
		i=$((i + 1))
		sleep 1
	done
	fail "$name did not become ready at $url"
}

wait_for_url 'Micronaut demo' "$APP_URL/health" "$APP_PID"
curl --noproxy '*' --max-time 5 -fsS "$APP_URL/health" |
    jq -e '.status == "UP"' >/dev/null

"$STATLITE_BIN" inspect --type micronaut "$APP_URL" >"$WORK_DIR/inspect.txt" 2>&1
grep -Fq 'type: micronaut' "$WORK_DIR/inspect.txt"
grep -Fq "$METRICS_URL" "$WORK_DIR/inspect.txt"
grep -Eq '^Compatibility: (compatible|partial)$' "$WORK_DIR/inspect.txt"

cat >"$STATLITE_CONFIG" <<EOF
server:
  listen: "127.0.0.1:19094"
storage:
  sqlite_path: "$WORK_DIR/statlite.sqlite"
polling:
  interval: "1h"
  timeout: "5s"
targets:
  - name: "micronaut-metrics-demo"
    type: "micronaut"
    url: "$METRICS_URL"
EOF
"$STATLITE_BIN" --config "$STATLITE_CONFIG" >"$STATLITE_LOG" 2>&1 &
STATLITE_PID=$!
wait_for_url StatLite "$STATLITE_URL/healthz" "$STATLITE_PID"

# /healthz can be ready before the startup collection finishes. The first
# counter baseline schedules a follow-up after 3s, even with a 1h interval.
# Require a successful stored poll and 10s of unchanged IDs: this exceeds
# the follow-up delay plus the configured 5s collection timeout.
settled_id=0
quiet_seconds=0
attempt=0
while [ "$quiet_seconds" -lt 10 ]; do
    [ "$attempt" -lt 40 ] || fail "startup polls did not settle"
    kill -0 "$STATLITE_PID" 2>/dev/null || fail "StatLite exited during startup settling"
    curl --noproxy '*' --max-time 2 -fsS "$STATLITE_URL/api/summary?range=1h" >"$WORK_DIR/startup-summary.json"
    current_id=$(jq -r '
        if .latest.status == "ok" and .monitor.last_successful_stored_poll_id > 0 and
            .latest.poll_id == .monitor.last_successful_stored_poll_id and
            .monitor.last_stored_poll_id == .latest.poll_id
        then .latest.poll_id else 0 end
    ' "$WORK_DIR/startup-summary.json")
    if [ "$current_id" -gt 0 ] && [ "$current_id" = "$settled_id" ]; then
        quiet_seconds=$((quiet_seconds + 1))
    else
        settled_id=$current_id
        quiet_seconds=0
    fi
    attempt=$((attempt + 1))
    if [ "$quiet_seconds" -lt 10 ]; then
        sleep 1
    fi
done
printf 'Startup polls settled at stored poll %s.\n' "$settled_id"

poll() {
    curl --noproxy '*' --max-time 10 -fsS "$STATLITE_URL/debug/poll-now" >"$WORK_DIR/$1.json"
    jq -e '.status == "ok" and .result.target_name == "micronaut-metrics-demo" and
        .result.health_status == "UP" and
        ((.result.db_health_status // "") == "")' "$WORK_DIR/$1.json" >/dev/null || fail "$1 collection failed"
    curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/summary?range=1h" >"$WORK_DIR/$1-summary.json"
    jq -e '.selected_target.name == "micronaut-metrics-demo" and .latest.status == "ok" and
        .monitor.last_successful_stored_poll_id > 0 and
        .latest.poll_id == .monitor.last_successful_stored_poll_id' "$WORK_DIR/$1-summary.json" >/dev/null || fail "$1 poll was not stored"
    jq -s -e '.[0].poll_id > 0 and .[0].poll_id == .[1].latest.poll_id and
        .[0].result.poll_finished_at == .[1].latest.result.poll_finished_at' \
        "$WORK_DIR/$1.json" "$WORK_DIR/$1-summary.json" >/dev/null || fail "$1 summary does not match the forced poll"
}
# Startup, its possible follow-up, and readiness traffic precede this baseline.
# After settling, the 1h interval keeps regular polls outside this short journey.
poll baseline
BASE_URL="$APP_URL" "$MICRONAUT_DIR/traffic.sh"
poll traffic

jq -s -e '
    def sample($p; $key): [$p.result.samples[] | select(.key == $key)][0].value;
    def delta($key): sample(.[1]; $key) - sample(.[0]; $key);
    delta("http_requests_total") >= 4 and
    delta("http_404_total") == 1 and delta("http_4xx_total") == 2 and delta("http_5xx_total") == 1 and
    delta("http_request_time_total_seconds") > 0 and
    (sample(.[1]; "process_cpu_usage") | type == "number" and . >= 0 and . <= 1) and
    sample(.[1]; "jvm_heap_used_bytes") > 0 and
    sample(.[1]; "process_start_time") > 0 and sample(.[1]; "process_uptime") > 0 and
    sample(.[1]; "process_start_time") == sample(.[0]; "process_start_time")
' "$WORK_DIR/baseline.json" "$WORK_DIR/traffic.json" >/dev/null || fail "unexpected counter/runtime samples"
# Management requests also enter HTTP timers; total is deliberately a lower
# bound because a scrape excludes its own unfinished request.
jq -s -e '.[1].latest.poll_id > .[0].latest.poll_id and
    .[1].latest.poll_id == (.[0].latest.poll_id + 1)' \
    "$WORK_DIR/baseline-summary.json" "$WORK_DIR/traffic-summary.json" >/dev/null || fail "unexpected stored poll boundary"
curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/series?range=1h" |
    jq -e '(.points | length) >= 2 and .latest_point.requests >= 4 and
        .latest_point.http_404 == 1 and .latest_point.http_4xx == 2 and
        .latest_point.http_5xx == 1 and .latest_point.average_latency_seconds > 0' >/dev/null ||
    fail "stored series did not expose the traffic deltas"
printf '%s\n' 'Micronaut integration passed.'
