#!/bin/sh
set -eu

# Check collection first, then the integration's own reported health.
# Exit 0 for OK, 1 for a configured condition, and 2 when data is unavailable.
BASE_URL=${BASE_URL:-http://127.0.0.1:9090}
TARGET=${TARGET:-my-app}
MAX_POLL_AGE_SECONDS=${MAX_POLL_AGE_SECONDS:-180}
MAX_HEALTH_AGE_SECONDS=${MAX_HEALTH_AGE_SECONDS:-300}
# Exact, comma-separated source health strings. Adjust for your integration.
UNHEALTHY_VALUES=${UNHEALTHY_VALUES:-DOWN,OUT_OF_SERVICE}

if ! response=$(curl -fsS --max-time 10 --get "${BASE_URL%/}/api/v1/status" --data-urlencode "target=$TARGET"); then
  echo "UNKNOWN: could not read status for $TARGET"
  exit 2
fi

if ! result=$(printf '%s' "$response" | jq -er \
  --arg target "$TARGET" --arg poll_age "$MAX_POLL_AGE_SECONDS" \
  --arg health_age "$MAX_HEALTH_AGE_SECONDS" --arg unhealthy "$UNHEALTHY_VALUES" '
  # jq date parsing needs whole seconds; the API can include fractional seconds.
  def epoch: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
  def age($time; $now): if $time == null then null else try ($now - ($time | epoch)) catch null end;
  (try ($poll_age | tonumber) catch null) as $max_poll |
  (try ($health_age | tonumber) catch null) as $max_health |
  now as $now |
  ($unhealthy | split(",")) as $bad |
  if $max_poll == null or $max_poll <= 0 or $max_health == null or $max_health <= 0 then
    "2:UNKNOWN: age limits must be positive numbers"
  elif .target != $target then "2:UNKNOWN: unexpected status target"
  elif .collection_status == "not_polled" then "2:UNKNOWN: target has not been polled"
  elif .collection_status != "ok" and .collection_status != "error" then
    "2:UNKNOWN: unrecognized collection status"
  elif (age(.last_poll_at; $now)) as $age | $age == null or $age < -30 or $age > $max_poll then
    "2:UNKNOWN: latest poll is unavailable or stale"
  elif .collection_status == "error" then
    "1:ALERT: latest collection failed (consecutive failures: \(.consecutive_collection_failures))"
  # Health may come from an older successful poll, so check its own timestamp.
  elif (age(.health_observed_at; $now)) as $age | $age == null or $age < -30 or $age > $max_health then
    "2:UNKNOWN: reported health is unavailable or stale"
  elif (.application_health as $app | .dependency_health as $dep |
        (($app != null and ($bad | index($app)) != null) or
         ($dep != null and ($bad | index($dep)) != null))) then
    "1:ALERT: reported health application=\(.application_health) dependency=\(.dependency_health)"
  elif .application_health == null then
    "2:UNKNOWN: application health is unreported"
  else
    # Keep an unreported dependency visible in an otherwise OK result.
    "0:OK: collection and configured health checks; application=\(.application_health) dependency=\(.dependency_health // "unreported")"
  end' 2>/dev/null); then
  echo "UNKNOWN: invalid status response or check configuration"
  exit 2
fi

case $result in
  0:*) echo "${result#0:}"; exit 0 ;;
  1:*) echo "${result#1:}"; exit 1 ;;
  *) echo "${result#2:}"; exit 2 ;;
esac
