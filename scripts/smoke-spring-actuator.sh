#!/bin/sh
set -eu

# Run the packaged Spring demo and a StatLite binary together, then verify the
# public integration boundary over HTTP. The caller is responsible for
# building both artifacts first.

SCRIPT_DIR=$(CDPATH=; cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH=; cd -- "$SCRIPT_DIR/.." && pwd)
SPRING_DIR="$REPO_DIR/examples/spring-actuator-demo"
STATLITE_BIN=${STATLITE_BIN:-"$REPO_DIR/statlite"}
SPRING_JAR=${SPRING_JAR:-}
SPRING_HOST=${SPRING_HOST:-127.0.0.1}
SPRING_PORT=${SPRING_PORT:-18080}
STATLITE_HOST=${STATLITE_HOST:-127.0.0.1}
STATLITE_PORT=${STATLITE_PORT:-19090}

WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/statlite-spring-smoke.XXXXXX")
SPRING_LOG="$WORK_DIR/spring.log"
STATLITE_LOG="$WORK_DIR/statlite.log"
SPRING_CONFIG="$WORK_DIR/statlite.yaml"
SPRING_URL="http://${SPRING_HOST}:${SPRING_PORT}"
STATLITE_URL="http://${STATLITE_HOST}:${STATLITE_PORT}"
SPRING_PID=
STATLITE_PID=

cleanup() {
	status=${1:-$?}
	trap - EXIT HUP INT TERM
	set +e
	if [ -n "$STATLITE_PID" ]; then
		kill "$STATLITE_PID" 2>/dev/null || true
		wait "$STATLITE_PID" 2>/dev/null || true
	fi
	if [ -n "$SPRING_PID" ]; then
		kill "$SPRING_PID" 2>/dev/null || true
		wait "$SPRING_PID" 2>/dev/null || true
	fi
	if [ "$status" -ne 0 ]; then
		printf '%s\n' '--- Spring application log ---' >&2
		tail -80 "$SPRING_LOG" >&2 || true
		printf '%s\n' '--- StatLite log ---' >&2
		tail -80 "$STATLITE_LOG" >&2 || true
	fi
	rm -rf "$WORK_DIR"
	exit "$status"
}

trap 'cleanup 129' HUP
trap 'cleanup 130' INT
trap 'cleanup 143' TERM
trap cleanup EXIT

fail() {
	printf 'smoke test failed: %s\n' "$*" >&2
	exit 1
}

[ -x "$STATLITE_BIN" ] || fail "StatLite binary is not executable: $STATLITE_BIN"

if [ -z "$SPRING_JAR" ]; then
	SPRING_JAR=$(find "$SPRING_DIR/target" -maxdepth 1 -type f -name '*.jar' ! -name '*-plain.jar' -print | head -n 1)
fi
[ -f "$SPRING_JAR" ] || fail "Spring Boot jar not found: $SPRING_JAR"

printf '%s\n' \
	'server:' \
	"  listen: \"${STATLITE_HOST}:${STATLITE_PORT}\"" \
	'' \
	'storage:' \
	"  sqlite_path: \"${WORK_DIR}/statlite-spring-demo.sqlite\"" \
	'  retention_days: 7' \
	'' \
	'polling:' \
	'  interval: "10s"' \
	'  timeout: "5s"' \
	'' \
	'targets:' \
	'  - name: "spring-demo"' \
	'    type: "spring"' \
	"    url: \"http://${SPRING_HOST}:${SPRING_PORT}/actuator\"" \
	>"$SPRING_CONFIG"

printf 'Starting Spring demo on %s\n' "$SPRING_URL"
java -jar "$SPRING_JAR" \
	--server.address="$SPRING_HOST" \
	--server.port="$SPRING_PORT" \
	>"$SPRING_LOG" 2>&1 &
SPRING_PID=$!

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

wait_for_url 'Spring demo' "$SPRING_URL/actuator/health" "$SPRING_PID"
curl --noproxy '*' --max-time 5 -fsS "$SPRING_URL/actuator/health" |
	jq -e '.status == "UP" and .components.db.status == "UP"' >/dev/null
curl --noproxy '*' --max-time 5 -fsS "$SPRING_URL/actuator/metrics/http.server.requests" |
	jq -e '.name == "http.server.requests"' >/dev/null

printf 'Generating Spring demo traffic\n'
(cd "$SPRING_DIR" && BASE_URL="$SPRING_URL" ./generate-traffic.sh)

printf 'Starting StatLite on %s\n' "$STATLITE_URL"
"$STATLITE_BIN" --config "$SPRING_CONFIG" >"$STATLITE_LOG" 2>&1 &
STATLITE_PID=$!

wait_for_url 'StatLite' "$STATLITE_URL/healthz" "$STATLITE_PID"
curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/healthz" |
	jq -e '.status == "ok" and .storage.status == "ok"' >/dev/null
curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/" |
	grep -Fq 'StatLite'
curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/statlite/metrics" |
	jq -e '.schema == "statlite-metrics/v1" and .status == "UP" and .database_status == "UP"' >/dev/null

# The explicit poll serializes with any startup poll, so the assertion does not
# depend on scheduler timing or the configured polling interval.
curl --noproxy '*' --max-time 10 -fsS "$STATLITE_URL/debug/poll-now" >"$WORK_DIR/traffic-poll.json"
if ! jq -e '
		.status == "ok" and
		.result.target_name == "spring-demo" and
		.result.health_status == "UP" and
		.result.db_health_status == "UP" and
		([.result.samples[] | select(.key == "http_requests_total" and .value >= 40)] | length) == 1 and
		([.result.samples[] | select(.key == "http_404_total" and .value >= 5)] | length) == 1 and
		([.result.samples[] | select(.key == "http_4xx_total" and .value >= 8)] | length) == 1 and
		([.result.samples[] | select(.key == "http_5xx_total" and .value >= 3)] | length) == 1
	' "$WORK_DIR/traffic-poll.json" >/dev/null; then
	printf '%s\n' 'Unexpected StatLite poll response:' >&2
	jq . "$WORK_DIR/traffic-poll.json" >&2 || cat "$WORK_DIR/traffic-poll.json" >&2
	fail 'StatLite did not collect the expected Spring metrics'
fi

curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/summary?range=1h" |
	jq -e '.selected_target.name == "spring-demo" and .latest.status == "ok" and .monitor.last_successful_stored_poll_id > 0' >/dev/null
curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/series?range=1h" |
	jq -e '(.points | length) > 0 and .latest_point != null' >/dev/null

printf '%s\n' 'Spring Actuator smoke test passed.'
