#!/bin/sh
set -eu

# Check process CPU cores across consecutive occupied UTC minute slots.
# Exit 0 for OK, 1 for sustained pressure, and 2 for missing evidence.
BASE_URL=${BASE_URL:-http://127.0.0.1:9090}
TARGET=${TARGET:-my-app}
CPU_CORES_THRESHOLD=${CPU_CORES_THRESHOLD:-0.80}
CONSECUTIVE_MINUTES=${CONSECUTIVE_MINUTES:-3}
MAX_POINT_AGE_SECONDS=${MAX_POINT_AGE_SECONDS:-180}

if ! response=$(curl -fsS --max-time 10 --get "${BASE_URL%/}/api/v1/metrics" --data-urlencode "target=$TARGET"); then
  echo "UNKNOWN: could not read metrics for $TARGET"
  exit 2
fi

if ! result=$(printf '%s' "$response" | jq -er \
  --arg target "$TARGET" --arg threshold "$CPU_CORES_THRESHOLD" \
  --arg minutes "$CONSECUTIVE_MINUTES" --arg age "$MAX_POINT_AGE_SECONDS" '
  # jq date parsing needs whole seconds; the API can include fractional seconds.
  def epoch: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
  (try ($threshold | tonumber) catch null) as $limit |
  (try ($minutes | tonumber) catch null) as $count |
  (try ($age | tonumber) catch null) as $max_age |
  if $limit == null or $limit < 0 or $count == null or $count < 2 or
     $count > 60 or $count != ($count | floor) or $max_age == null or $max_age <= 0 then
    "2:UNKNOWN: invalid CPU threshold, minute count, or age setting"
  elif .target != $target then "2:UNKNOWN: unexpected metrics target"
  else
    # The API orders points chronologically; inspect its latest occupied slots.
    (.points | .[-$count:]) as $recent |
    if ($recent | length) < $count then
      "2:UNKNOWN: too few occupied minute points for sustained CPU check"
    elif (try ((.evaluated_at | epoch) - ($recent[-1].timestamp | epoch)) catch null) as $elapsed |
         $elapsed == null or $elapsed < -30 or $elapsed > $max_age then
      "2:UNKNOWN: latest CPU point is stale"
    # Sparse points cannot prove consecutive minutes of sustained CPU usage.
    elif ([range(1; $count) | select(($recent[.].timestamp | epoch) -
                                    ($recent[. - 1].timestamp | epoch) != 60)] | length) > 0 then
      "2:UNKNOWN: occupied minute points have a gap"
    elif any($recent[]; .process_cpu_cores == null) then
      "2:UNKNOWN: CPU measurement is unavailable in a required minute"
    elif all($recent[]; .process_cpu_cores > $limit) then
      "1:ALERT: process CPU exceeded \($limit) cores in \($count) consecutive minute points"
    else
      "0:OK: process CPU did not exceed \($limit) cores in all \($count) consecutive minute points"
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
