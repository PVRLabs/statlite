#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH=; cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH=; cd -- "$SCRIPT_DIR/../.." && pwd)
CASE=${1:-}
STATLITE_BIN=${STATLITE_BIN:-"$REPO_DIR/statlite"}
PYTHON_BIN=${PYTHON_BIN:-python}

WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/statlite-direct-v1.XXXXXX")
APP_LOG="$WORK_DIR/application.log"
STATLITE_LOG="$WORK_DIR/statlite.log"
POLL_JSON="$WORK_DIR/poll.json"
touch "$APP_LOG" "$STATLITE_LOG"
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
	stop_process application "$APP_PID"
	if [ "$status" -ne 0 ]; then
		printf '%s\n' '--- application log ---' >&2
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
	printf 'direct-v1 integration failed: %s\n' "$*" >&2
	exit 1
}

[ -x "$STATLITE_BIN" ] || fail "StatLite binary is not executable: $STATLITE_BIN"

# Bare integration IDs identify StatLite-maintained, documented helpers. Keep
# integration_version absent unless behavior or compatibility must be
# distinguished; do not assign behavior to third-party IDs without namespacing.
case "$CASE" in
fastapi)
	APP_DIR="$REPO_DIR/examples/python-fastapi-demo"
	CONFIG_TEMPLATE="$APP_DIR/statlite.yaml"
	CONFIG_APP_PORT=8000
	APP_PORT=${APP_PORT:-$CONFIG_APP_PORT}
	APP_URL="http://127.0.0.1:$APP_PORT"
	METRICS_URL="$APP_URL/statlite/metrics"
	TARGET_NAME=python-fastapi-demo
	INTEGRATION_ID=fastapi
	(
		cd "$APP_DIR"
		exec "$PYTHON_BIN" -m uvicorn app:app --host 127.0.0.1 --port "$APP_PORT" --workers 1
	) >"$APP_LOG" 2>&1 &
	APP_PID=$!
	;;
express)
	APP_DIR="$REPO_DIR/examples/node-express-demo"
	CONFIG_TEMPLATE="$APP_DIR/statlite.yaml"
	CONFIG_APP_PORT=3000
	APP_PORT=${APP_PORT:-$CONFIG_APP_PORT}
	APP_URL="http://127.0.0.1:$APP_PORT"
	METRICS_URL="$APP_URL/statlite/metrics"
	TARGET_NAME=node-express-demo
	INTEGRATION_ID=express
	(
		cd "$APP_DIR"
		PORT="$APP_PORT" exec node app.js
	) >"$APP_LOG" 2>&1 &
	APP_PID=$!
	;;
django)
	APP_DIR="$REPO_DIR/examples/python-django-demo"
	CONFIG_TEMPLATE="$APP_DIR/statlite.yaml"
	CONFIG_APP_PORT=8000
	APP_PORT=${APP_PORT:-$CONFIG_APP_PORT}
	APP_URL="http://127.0.0.1:$APP_PORT"
	METRICS_URL="$APP_URL/statlite/metrics"
	TARGET_NAME=python-django-demo
	INTEGRATION_ID=django
	(
		cd "$APP_DIR"
		exec "$PYTHON_BIN" manage.py runserver "127.0.0.1:$APP_PORT" --noreload
	) >"$APP_LOG" 2>&1 &
	APP_PID=$!
	;;
*)
	fail "unknown integration case: $CASE"
	;;
esac

wait_for_url() {
	name=$1
	url=$2
	i=0
	while [ "$i" -lt 60 ]; do
		if curl --noproxy '*' --max-time 2 -fsS "$url" >/dev/null 2>&1; then
			return 0
		fi
		if ! kill -0 "$3" 2>/dev/null; then
			fail "$name exited before becoming ready"
		fi
		i=$((i + 1))
		sleep 1
	done
	fail "$name did not become ready at $url"
}

expect_status() {
	want=$1
	url=$2
	got=$(curl --noproxy '*' --max-time 5 -sS -o /dev/null -w '%{http_code}' "$url" || printf '000')
	[ "$got" = "$want" ] || fail "$url returned HTTP $got, want $want"
}

wait_for_url application "$METRICS_URL" "$APP_PID"

inspect_output="$WORK_DIR/inspect.txt"
"$STATLITE_BIN" inspect "$APP_URL" >"$inspect_output" 2>&1 || fail "statlite inspect failed"
grep -Fq 'Detected: StatLite Metrics v1' "$inspect_output" || fail "statlite inspect did not recognize $CASE"

expect_status 200 "$APP_URL/"
expect_status 404 "$APP_URL/missing"
expect_status 500 "$APP_URL/failure"
curl --noproxy '*' --max-time 5 -fsS "$METRICS_URL" >"$WORK_DIR/application-metrics.json"
curl --noproxy '*' --max-time 5 -fsS "$METRICS_URL?source=exclusion-check" >"$WORK_DIR/application-metrics-after.json"
jq -s -e '
	.[0].metrics.requests_total == .[1].metrics.requests_total and
	.[0].metrics.responses_404_total == .[1].metrics.responses_404_total and
	.[0].metrics.responses_4xx_total == .[1].metrics.responses_4xx_total and
	.[0].metrics.responses_5xx_total == .[1].metrics.responses_5xx_total and
	.[0].metrics.request_duration_seconds_total == .[1].metrics.request_duration_seconds_total
' "$WORK_DIR/application-metrics.json" "$WORK_DIR/application-metrics-after.json" >/dev/null ||
	fail "metrics endpoint polling changed application counters"
jq -e --arg integration "$INTEGRATION_ID" '
	.schema == "statlite-metrics/v1" and
	.integration == $integration and
	(.status | type == "string" and length > 0) and
	.metrics.requests_total >= 3 and
	.metrics.responses_404_total >= 1 and
	.metrics.responses_4xx_total >= 1 and
	.metrics.responses_5xx_total >= 1 and
	.metrics.request_duration_seconds_total > 0
' "$WORK_DIR/application-metrics.json" >/dev/null || fail "application metrics did not contain the expected basic signals"

STATLITE_CONFIG="$WORK_DIR/statlite.yaml"
sed \
	-e 's#listen: "127.0.0.1:9090"#listen: "127.0.0.1:19091"#' \
	-e "s#127.0.0.1:$CONFIG_APP_PORT/statlite/metrics#127.0.0.1:$APP_PORT/statlite/metrics#" \
	-e "s#sqlite_path:.*#sqlite_path: \"$WORK_DIR/statlite.sqlite\"#" \
	"$CONFIG_TEMPLATE" >"$STATLITE_CONFIG"

"$STATLITE_BIN" --config "$STATLITE_CONFIG" >"$STATLITE_LOG" 2>&1 &
STATLITE_PID=$!
STATLITE_URL=http://127.0.0.1:19091
wait_for_url StatLite "$STATLITE_URL/healthz" "$STATLITE_PID"

curl --noproxy '*' --max-time 10 -fsS "$STATLITE_URL/debug/poll-now" >"$POLL_JSON"
jq -e \
	--arg target "$TARGET_NAME" \
	'.status == "ok" and .result.target_name == $target and .result.health_status == "UP" and
	([.result.samples[] | select(.key == "http_requests_total" and .value >= 3)] | length) == 1 and
	([.result.samples[] | select(.key == "http_404_total" and .value >= 1)] | length) == 1 and
	([.result.samples[] | select(.key == "http_4xx_total" and .value >= 1)] | length) == 1 and
	([.result.samples[] | select(.key == "http_5xx_total" and .value >= 1)] | length) == 1 and
	([.result.samples[] | select(.key == "http_request_time_total_seconds" and .value > 0)] | length) == 1' \
	"$POLL_JSON" >/dev/null || fail "StatLite did not collect the expected normalized metrics"

curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/summary?range=1h" |
	jq -e \
		--arg target "$TARGET_NAME" \
		'.selected_target.name == $target and .latest.status == "ok" and .monitor.last_successful_stored_poll_id > 0' >/dev/null ||
	fail "StatLite summary did not expose the stored result"
curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/series?range=1h" |
	jq -e '(.points | length) > 0 and .latest_point != null' >/dev/null ||
	fail "StatLite series did not expose visible stored metrics"

printf 'Direct-v1 integration passed: %s\n' "$CASE"
