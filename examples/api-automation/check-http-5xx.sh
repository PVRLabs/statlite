#!/bin/sh
set -eu

# Check a recent 5xx spike only when enough requests and paired status data exist.
# Exit 0 for OK, 1 for a spike, and 2 when the rate cannot be evaluated.
BASE_URL=${BASE_URL:-http://127.0.0.1:9090}
TARGET=${TARGET:-my-app}
MAX_HTTP_AGE_SECONDS=${MAX_HTTP_AGE_SECONDS:-180}
MIN_REQUESTS=${MIN_REQUESTS:-20}
MAX_5XX_RATE=${MAX_5XX_RATE:-0.05}

if ! response=$(curl -fsS --max-time 10 --get "${BASE_URL%/}/api/v1/metrics" --data-urlencode "target=$TARGET"); then
  echo "UNKNOWN: could not read metrics for $TARGET"
  exit 2
fi

if ! result=$(printf '%s' "$response" | jq -er \
  --arg target "$TARGET" --arg age "$MAX_HTTP_AGE_SECONDS" \
  --arg minimum "$MIN_REQUESTS" --arg threshold "$MAX_5XX_RATE" '
  # jq date parsing needs whole seconds; the API can include fractional seconds.
  def epoch: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
  (try ($age | tonumber) catch null) as $max_age |
  (try ($minimum | tonumber) catch null) as $min_requests |
  (try ($threshold | tonumber) catch null) as $max_rate |
  if $max_age == null or $max_age <= 0 or $min_requests == null or $min_requests <= 0 or
     $max_rate == null or $max_rate < 0 or $max_rate > 1 then
    "2:UNKNOWN: invalid age, traffic, or rate setting"
  elif .target != $target then "2:UNKNOWN: unexpected metrics target"
  elif .latest_http_observation_at == null then "2:UNKNOWN: no usable HTTP observation"
  # Use the source poll time for freshness; the point timestamp is minute-aligned.
  elif (try ((.evaluated_at | epoch) - (.latest_http_observation_at | epoch)) catch null) as $elapsed |
       $elapsed == null or $elapsed < -30 or $elapsed > $max_age then
    "2:UNKNOWN: HTTP observation is stale"
  else
    # Select the newest HTTP slot; a newer resource-only slot may exist.
    ([.points[] | select(.requests != null or .avg_latency_ms != null or
                        .http_4xx != null or .http_5xx != null)] | last) as $point |
    if $point == null then "2:UNKNOWN: no usable HTTP point"
    elif $point.requests == null or $point.requests < $min_requests then
      "2:UNKNOWN: recent HTTP point has insufficient requests"
    # A null rate means incomplete paired 5xx coverage, not zero errors.
    elif $point.http_5xx_rate == null then
      "2:UNKNOWN: 5xx rate is unavailable for the recent HTTP point"
    elif $point.http_5xx_rate > $max_rate then
      "1:ALERT: 5xx rate \($point.http_5xx_rate) exceeds \($max_rate) across \($point.requests) requests"
    else
      "0:OK: 5xx rate \($point.http_5xx_rate) across \($point.requests) requests"
    end
  end' 2>/dev/null); then
  echo "UNKNOWN: invalid metrics response or check configuration"
  exit 2
fi

case $result in
  0:*) echo "${result#0:}"; exit 0 ;;
  1:*) echo "${result#1:}"; exit 1 ;;
  *) echo "${result#2:}"; exit 2 ;;
esac
