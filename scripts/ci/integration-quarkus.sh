#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH=; cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH=; cd -- "$SCRIPT_DIR/../.." && pwd)
QUARKUS_DIR="$REPO_DIR/examples/quarkus-metrics-demo"
STATLITE_BIN=${STATLITE_BIN:-"$REPO_DIR/statlite"}

WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/statlite-quarkus.XXXXXX")
APP_LOG="$WORK_DIR/quarkus.log"
STATLITE_LOG="$WORK_DIR/statlite.log"
POLL_JSON="$WORK_DIR/poll.json"
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
	stop_process Quarkus "$APP_PID"
	if [ "$status" -ne 0 ]; then
		printf '%s\n' '--- Quarkus application log ---' >&2
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
	printf 'Quarkus integration failed: %s\n' "$*" >&2
	exit 1
}

[ -x "$STATLITE_BIN" ] || fail "StatLite binary is not executable: $STATLITE_BIN"

APP_URL=http://127.0.0.1:8080
METRICS_URL=http://127.0.0.1:9000/q/metrics
STATLITE_URL=http://127.0.0.1:19091

java -jar "$QUARKUS_DIR/target/quarkus-app/quarkus-run.jar" >"$APP_LOG" 2>&1 &
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

wait_for_url 'Quarkus demo' http://127.0.0.1:9000/q/health "$APP_PID"
curl --noproxy '*' --max-time 5 -fsS http://127.0.0.1:9000/q/health |
	jq -e '.status == "UP"' >/dev/null
curl --noproxy '*' --max-time 5 -fsS -H 'Accept: text/plain' "$METRICS_URL" |
	grep -q 'process_start_time_seconds\|jvm_memory_used_bytes'

"$STATLITE_BIN" inspect --type quarkus "$METRICS_URL" >"$WORK_DIR/inspect.txt" 2>&1
grep -Fq 'Detected: Quarkus Metrics' "$WORK_DIR/inspect.txt"

(cd "$QUARKUS_DIR" && BASE_URL="$APP_URL" ./traffic.sh)

printf '%s\n' \
	'server:' \
	'  listen: "127.0.0.1:19091"' \
	'' \
	'storage:' \
	"  sqlite_path: \"$WORK_DIR/statlite.sqlite\"" \
	'' \
	'polling:' \
	'  interval: "1h"' \
	'  timeout: "5s"' \
	'' \
	'targets:' \
	'  - name: "quarkus-metrics-demo"' \
	'    type: "quarkus"' \
	"    url: \"$METRICS_URL\"" \
	>"$STATLITE_CONFIG"

"$STATLITE_BIN" --config "$STATLITE_CONFIG" >"$STATLITE_LOG" 2>&1 &
STATLITE_PID=$!
wait_for_url StatLite "$STATLITE_URL/healthz" "$STATLITE_PID"
curl --noproxy '*' --max-time 10 -fsS "$STATLITE_URL/debug/poll-now" >"$POLL_JSON"
jq -e '
	.status == "ok" and .result.target_name == "quarkus-metrics-demo" and
	.result.health_status == "UP" and
	([.result.samples[] | select(.key == "http_requests_total" and .value >= 4)] | length) == 1 and
	([.result.samples[] | select(.key == "http_404_total" and .value >= 1)] | length) == 1 and
	([.result.samples[] | select(.key == "http_4xx_total" and .value >= 2)] | length) == 1 and
	([.result.samples[] | select(.key == "http_5xx_total" and .value >= 1)] | length) == 1 and
	([.result.samples[] | select(.key == "http_request_time_total_seconds" and .value > 0)] | length) == 1 and
	([.result.samples[] | select(.key == "process_cpu_usage" and .value >= 0)] | length) == 1 and
	([.result.samples[] | select(.key == "jvm_heap_used_bytes" and .value > 0)] | length) == 1
' "$POLL_JSON" >/dev/null || fail "StatLite did not collect the expected Quarkus metrics"

curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/summary?range=1h" |
	jq -e '.selected_target.name == "quarkus-metrics-demo" and .latest.status == "ok" and .monitor.last_successful_stored_poll_id > 0' >/dev/null ||
	fail "StatLite summary did not expose the stored Quarkus result"
curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/series?range=1h" |
	jq -e '(.points | length) > 0 and .latest_point != null' >/dev/null ||
	fail "StatLite series did not expose visible Quarkus metrics"

printf '%s\n' 'Quarkus integration passed.'
