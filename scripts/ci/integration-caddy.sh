#!/bin/sh
set -eu

# Smoke the documented Caddy named-site shape: pinned Caddy 2.11.4, per_host
# metrics on the loopback admin endpoint, and two Python standard-library
# upstreams. StatLite starts before either site has traffic, so the startup
# poll fails with caddy_http_not_observed and does not schedule the 3s
# counter follow-up. Later polls are explicit. The check then requires exact
# host-isolated deltas. It does not exercise HTTPS or the Docker metrics listener.

SCRIPT_DIR=$(CDPATH=; cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH=; cd -- "$SCRIPT_DIR/../.." && pwd)
STATLITE_BIN=${STATLITE_BIN:-"$REPO_DIR/statlite"}
PYTHON_BIN=${PYTHON_BIN:-python3}
CADDY_VERSION=2.11.4
ALPHA_PORT=28180
BETA_PORT=28181
PROXY_PORT=28182
ADMIN_PORT=28183
STATLITE_PORT=19095

WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/statlite-caddy.XXXXXX")
ALPHA_LOG="$WORK_DIR/alpha.log"
BETA_LOG="$WORK_DIR/beta.log"
CADDY_LOG="$WORK_DIR/caddy.log"
STATLITE_LOG="$WORK_DIR/statlite.log"
METRICS_FILE="$WORK_DIR/metrics.prom"
ALPHA_PID=
BETA_PID=
CADDY_PID=
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
	stop_process Caddy "$CADDY_PID"
	stop_process 'beta upstream' "$BETA_PID"
	stop_process 'alpha upstream' "$ALPHA_PID"
	if [ "$status" -ne 0 ]; then
		printf '%s\n' '--- alpha upstream log ---' >&2
		tail -40 "$ALPHA_LOG" >&2 || true
		printf '%s\n' '--- beta upstream log ---' >&2
		tail -40 "$BETA_LOG" >&2 || true
		printf '%s\n' '--- Caddy log ---' >&2
		tail -80 "$CADDY_LOG" >&2 || true
		printf '%s\n' '--- Caddy host metrics ---' >&2
		grep 'caddy_http_request_duration_seconds_count{' "$METRICS_FILE" >&2 || true
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
	printf 'caddy smoke test failed: %s\n' "$*" >&2
	exit 1
}

[ -x "$STATLITE_BIN" ] || fail "StatLite binary is not executable: $STATLITE_BIN"
command -v "$PYTHON_BIN" >/dev/null 2>&1 || fail "Python is not available: $PYTHON_BIN"
command -v curl >/dev/null 2>&1 || fail "curl is required"
command -v jq >/dev/null 2>&1 || fail "jq is required"

case "$(uname -s)-$(uname -m)" in
Darwin-x86_64)
	caddy_artifact="caddy_${CADDY_VERSION}_mac_amd64.tar.gz"
	caddy_checksum="e04eb10f9ce7e2e079bc9bff1bd5d3a3164888d1edbb1a49e5d15be4eab691b57e89ed36bb29c65ba43f1ba8d9279e0967b1003991c13fe4cb78384c3caf25de"
	;;
Darwin-arm64)
	caddy_artifact="caddy_${CADDY_VERSION}_mac_arm64.tar.gz"
	caddy_checksum="3190ae0df98b59ab4b6021556fa35adc3c526a4f3e138776b0eaec8a037cc26121cbbb1ad53453f565551b47d37d5ba4755e2c2c3652256737fe2ce9e53c8ec0"
	;;
Linux-x86_64)
	caddy_artifact="caddy_${CADDY_VERSION}_linux_amd64.tar.gz"
	caddy_checksum="8220d1f013b6f27510247b2360c9e0ca9f018feebd82515f07635318b34ff9777ccc8fd0b6e6f2486ce3a33fe389fbb7db12d05baa474f4587509fb4f5ebf1c9"
	;;
*)
	fail "unsupported platform: $(uname -s)-$(uname -m)"
	;;
esac

printf 'Downloading Caddy %s\n' "$CADDY_VERSION"
caddy_archive="$WORK_DIR/$caddy_artifact"
curl --noproxy '*' -fL --retry 3 --max-time 60 -o "$caddy_archive" \
	"https://github.com/caddyserver/caddy/releases/download/v${CADDY_VERSION}/${caddy_artifact}"
if command -v shasum >/dev/null 2>&1; then
	actual_checksum=$(shasum -a 512 "$caddy_archive" | awk '{print $1}')
else
	actual_checksum=$(sha512sum "$caddy_archive" | awk '{print $1}')
fi
[ "$actual_checksum" = "$caddy_checksum" ] || fail "checksum mismatch for $caddy_artifact"
tar -xzf "$caddy_archive" -C "$WORK_DIR" caddy
CADDY_BIN="$WORK_DIR/caddy"
chmod +x "$CADDY_BIN"
"$CADDY_BIN" version | grep -F "v${CADDY_VERSION}" >/dev/null || fail "Caddy binary is not v${CADDY_VERSION}"

cat >"$WORK_DIR/upstream.py" <<'PY'
import signal
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))

marker = sys.argv[2]

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def handle_request(self):
        status = {"/bad": 400, "/missing": 404, "/fail": 500}.get(self.path.split("?", 1)[0], 200)
        body = f"{marker} {status}\n".encode()
        self.send_response(status)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.close_connection = True
        if self.command != "HEAD":
            self.wfile.write(body)

    do_GET = handle_request
    do_HEAD = handle_request
    do_POST = handle_request

    def log_message(self, format, *args):
        return

class Server(ThreadingHTTPServer):
    allow_reuse_address = True

Server(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
PY

printf '%s\n' \
	'{' \
	"	admin 127.0.0.1:${ADMIN_PORT}" \
	'	persist_config off' \
	'	auto_https off' \
	'	metrics {' \
	'		per_host' \
	'	}' \
	'}' \
	'' \
	"http://alpha.test:${PROXY_PORT} {" \
	"	reverse_proxy 127.0.0.1:${ALPHA_PORT}" \
	'}' \
	"http://beta.test:${PROXY_PORT} {" \
	"	reverse_proxy 127.0.0.1:${BETA_PORT}" \
	'}' \
	>"$WORK_DIR/Caddyfile"

wait_for_url() {
	name=$1
	url=$2
	pid=$3
	i=0
	while [ "$i" -lt 50 ]; do
		if curl --noproxy '*' --max-time 2 -fsS "$url" >/dev/null 2>&1; then
			return 0
		fi
		if ! kill -0 "$pid" 2>/dev/null; then
			fail "$name exited before becoming ready"
		fi
		i=$((i + 1))
		sleep 0.2
	done
	fail "$name did not become ready at $url"
}

request() {
	host=$1
	path=$2
	expect=$3
	marker=${host%%.*}
	code=$(curl --noproxy '*' --max-time 5 -sS -o "$WORK_DIR/response.txt" -w '%{http_code}' \
		-H "Host: ${host}" "http://127.0.0.1:${PROXY_PORT}${path}") || fail "request to ${host}${path} failed"
	[ "$code" = "$expect" ] || fail "${host}${path} returned ${code}, want ${expect}"
	grep -Fxq "${marker} ${expect}" "$WORK_DIR/response.txt" ||
		fail "${host}${path} reached the wrong upstream: $(cat "$WORK_DIR/response.txt")"
}

printf 'Starting Python upstreams\n'
"$PYTHON_BIN" "$WORK_DIR/upstream.py" "$ALPHA_PORT" alpha >"$ALPHA_LOG" 2>&1 &
ALPHA_PID=$!
"$PYTHON_BIN" "$WORK_DIR/upstream.py" "$BETA_PORT" beta >"$BETA_LOG" 2>&1 &
BETA_PID=$!
wait_for_url 'alpha upstream' "http://127.0.0.1:${ALPHA_PORT}/ok" "$ALPHA_PID"
wait_for_url 'beta upstream' "http://127.0.0.1:${BETA_PORT}/ok" "$BETA_PID"

printf 'Starting Caddy\n'
mkdir -p "$WORK_DIR/xdg-data" "$WORK_DIR/xdg-config"
XDG_DATA_HOME="$WORK_DIR/xdg-data" XDG_CONFIG_HOME="$WORK_DIR/xdg-config" \
	"$CADDY_BIN" run --config "$WORK_DIR/Caddyfile" --adapter caddyfile >"$CADDY_LOG" 2>&1 &
CADDY_PID=$!
wait_for_url Caddy "http://127.0.0.1:${ADMIN_PORT}/metrics" "$CADDY_PID"

STATLITE_URL="http://127.0.0.1:${STATLITE_PORT}"
cat >"$WORK_DIR/statlite.yaml" <<EOF
server:
  listen: "127.0.0.1:${STATLITE_PORT}"

storage:
  sqlite_path: "${WORK_DIR}/statlite.sqlite"
  retention_days: 7

polling:
  interval: "1h"
  timeout: "5s"

targets:
  - name: alpha
    type: caddy
    url: "http://127.0.0.1:${ADMIN_PORT}/metrics"
    host: alpha.test
  - name: beta
    type: caddy
    url: "http://127.0.0.1:${ADMIN_PORT}/metrics"
    host: beta.test
EOF

printf 'Starting StatLite\n'
"$STATLITE_BIN" --config "$WORK_DIR/statlite.yaml" >"$STATLITE_LOG" 2>&1 &
STATLITE_PID=$!
wait_for_url StatLite "$STATLITE_URL/healthz" "$STATLITE_PID"

# /healthz can be ready before startup collection finishes. Wait until both
# targets have recorded the missing-metrics failure. That poll does not arm
# the successful-baseline follow-up.
expect_unobserved() {
	name=$1
	i=0
	while [ "$i" -lt 40 ]; do
		kill -0 "$STATLITE_PID" 2>/dev/null || fail "StatLite exited before ${name} startup collection"
		curl --noproxy '*' --max-time 2 -fsS "$STATLITE_URL/api/summary?range=1h&target=${name}" >"$WORK_DIR/${name}-unobserved-summary.json" || true
		if jq -e --arg name "$name" '
			.selected_target.name == $name and
			.latest.status == "error" and
			.monitor.consecutive_poll_failures >= 1 and
			([.latest.result.events[]? | select(.type == "caddy_http_not_observed")] | length) == 1
		' "$WORK_DIR/${name}-unobserved-summary.json" >/dev/null 2>&1; then
			return 0
		fi
		i=$((i + 1))
		sleep 0.25
	done
	fail "${name} startup poll did not report caddy_http_not_observed"
}

expect_unobserved alpha
expect_unobserved beta
for name in alpha beta; do
	status=$(curl --noproxy '*' --max-time 10 -sS -o "$WORK_DIR/${name}-unobserved.json" -w '%{http_code}' \
		"$STATLITE_URL/debug/poll-now?target=${name}") || fail "unobserved poll ${name} failed"
	[ "$status" = "502" ] || fail "unobserved poll ${name} returned HTTP ${status}, want 502"
	jq -e --arg name "$name" '
		.status == "error" and .result.target_name == $name and
		([.result.events[]? | select(.type == "caddy_http_not_observed")] | length) == 1
	' "$WORK_DIR/${name}-unobserved.json" >/dev/null ||
		fail "unobserved poll ${name} did not report caddy_http_not_observed"
done

# The host label is the Host header without the listener port. Prime both
# sites so the next poll can observe a per-host HTTP population.
request alpha.test /ok 200
request beta.test /ok 200
curl --noproxy '*' --max-time 5 -fsS "http://127.0.0.1:${ADMIN_PORT}/metrics" >"$METRICS_FILE"
grep -F 'host="alpha.test"' "$METRICS_FILE" >/dev/null || fail 'Caddy did not emit alpha.test HTTP metrics'
grep -F 'host="beta.test"' "$METRICS_FILE" >/dev/null || fail 'Caddy did not emit beta.test HTTP metrics'

poll_target() {
	name=$1
	out=$2
	status=$(curl --noproxy '*' --max-time 10 -sS -o "$out" -w '%{http_code}' \
		"$STATLITE_URL/debug/poll-now?target=${name}") || fail "poll ${name} failed"
	[ "$status" = "200" ] || fail "poll ${name} returned HTTP ${status}"
	jq -e --arg name "$name" \
		'.status == "ok" and .result.target_name == $name and (.result.samples | length) == 5' \
		"$out" >/dev/null || fail "poll ${name} did not collect Caddy counters"
}

# Startup already failed closed, so these explicit polls are adjacent.
poll_target alpha "$WORK_DIR/alpha-baseline.json"
poll_target beta "$WORK_DIR/beta-baseline.json"

request alpha.test /ok 200
request alpha.test /missing 404
request alpha.test /bad 400
request alpha.test /fail 500
request beta.test /ok 200

poll_target alpha "$WORK_DIR/alpha-traffic.json"
poll_target beta "$WORK_DIR/beta-traffic.json"

assert_delta() {
	name=$1
	requests=$2
	not_found=$3
	client_errors=$4
	server_errors=$5
	jq -s -e \
		--argjson requests "$requests" \
		--argjson not_found "$not_found" \
		--argjson client_errors "$client_errors" \
		--argjson server_errors "$server_errors" '
		def sample($key): [.result.samples[] | select(.key == $key)][0].value;
		(.[1] | sample("http_requests_total")) - (.[0] | sample("http_requests_total")) == $requests and
		(.[1] | sample("http_404_total")) - (.[0] | sample("http_404_total")) == $not_found and
		(.[1] | sample("http_4xx_total")) - (.[0] | sample("http_4xx_total")) == $client_errors and
		(.[1] | sample("http_5xx_total")) - (.[0] | sample("http_5xx_total")) == $server_errors and
		(.[1] | sample("http_request_time_total_seconds")) - (.[0] | sample("http_request_time_total_seconds")) > 0
	' "$WORK_DIR/${name}-baseline.json" "$WORK_DIR/${name}-traffic.json" >/dev/null ||
		fail "${name} counter deltas did not match the traffic sent to that host"
}

assert_delta alpha 4 1 2 1
assert_delta beta 1 0 0 0

assert_series() {
	name=$1
	requests=$2
	not_found=$3
	client_errors=$4
	server_errors=$5
	curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/summary?range=1h&target=${name}" |
		jq -e --arg name "$name" \
			'.selected_target.name == $name and .latest.status == "ok" and .monitor.last_successful_stored_poll_id > 0' \
			>/dev/null || fail "${name} summary did not expose the stored poll"
	curl --noproxy '*' --max-time 5 -fsS "$STATLITE_URL/api/series?range=1h&target=${name}" |
		jq -e \
			--argjson requests "$requests" \
			--argjson not_found "$not_found" \
			--argjson client_errors "$client_errors" \
			--argjson server_errors "$server_errors" '
			(.points | length) >= 2 and
			.latest_point != null and
			.latest_point.requests == $requests and
			.latest_point.http_404 == $not_found and
			.latest_point.http_4xx == $client_errors and
			.latest_point.http_5xx == $server_errors and
			.latest_point.average_latency_seconds > 0
		' >/dev/null || fail "${name} series did not expose the traffic delta"
}

assert_series alpha 4 1 2 1
assert_series beta 1 0 0 0

printf '%s\n' 'Caddy smoke test passed.'
