#!/bin/sh
set -eu

# Check host memory and disk fractions reported by the selected target.
# Exit 0 for OK, 1 for pressure, and 2 when an all-clear is unsupported.
BASE_URL=${BASE_URL:-http://127.0.0.1:9090}
# statlite-self observes only the environment visible to StatLite.
TARGET=${TARGET:-statlite-self}
MAX_POINT_AGE_SECONDS=${MAX_POINT_AGE_SECONDS:-180}
MAX_MEMORY_USAGE=${MAX_MEMORY_USAGE:-0.90}
MAX_DISK_USAGE=${MAX_DISK_USAGE:-0.90}

if ! response=$(curl -fsS --max-time 10 --get "${BASE_URL%/}/api/v1/metrics" --data-urlencode "target=$TARGET"); then
  echo "UNKNOWN: could not read metrics for $TARGET"
  exit 2
fi

if ! result=$(printf '%s' "$response" | jq -er \
  --arg target "$TARGET" --arg age "$MAX_POINT_AGE_SECONDS" \
  --arg memory "$MAX_MEMORY_USAGE" --arg disk "$MAX_DISK_USAGE" '
  # jq date parsing needs whole seconds; the API can include fractional seconds.
  def epoch: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
  (try ($age | tonumber) catch null) as $max_age |
  (try ($memory | tonumber) catch null) as $max_memory |
  (try ($disk | tonumber) catch null) as $max_disk |
  if $max_age == null or $max_age <= 0 or $max_memory == null or $max_memory < 0 or
     $max_memory > 1 or $max_disk == null or $max_disk < 0 or $max_disk > 1 then
    "2:UNKNOWN: invalid age or resource threshold"
  elif .target != $target then "2:UNKNOWN: unexpected metrics target"
  else
    # Select the newest host slot; a newer CPU-only slot may exist.
    ([.points[] | select(.host_memory_usage != null or .host_disk_usage != null)] | last) as $point |
    if $point == null then "2:UNKNOWN: no host memory or disk observation"
    elif (try ((.evaluated_at | epoch) - ($point.timestamp | epoch)) catch null) as $elapsed |
         $elapsed == null or $elapsed < -30 or $elapsed > $max_age then
      "2:UNKNOWN: host resource point is stale"
    elif ($point.host_memory_usage != null and $point.host_memory_usage > $max_memory) or
         ($point.host_disk_usage != null and $point.host_disk_usage > $max_disk) then
      "1:ALERT: host memory=\($point.host_memory_usage) disk=\($point.host_disk_usage)"
    # One missing field still leaves a possible pressure condition unchecked.
    elif $point.host_memory_usage == null or $point.host_disk_usage == null then
      "2:UNKNOWN: host memory or disk usage is unavailable"
    else
      "0:OK: host memory=\($point.host_memory_usage) disk=\($point.host_disk_usage)"
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
