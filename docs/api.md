# External API

StatLite's external API is a small, read-only interface for scripts and other
automation clients. The current contract is API v1. It reports one configured
target's collection and source-reported health, recent collector events, and a
fixed rolling hour of derived metrics. Requests do not start a poll or change
stored data.

The dashboard's other `/api/*` routes are internal and are not part of this
contract. Callers own their thresholds, schedules, deduplication, and
notification delivery. This API does not implement alert rules or delivery.

## Access and target selection

All endpoints are `GET` requests. Bind StatLite to loopback or protect access
with deployment-level network controls or a reverse proxy. StatLite does not
provide API authentication; do not expose the listener to an untrusted
network.

Each endpoint accepts an optional `target` query parameter. If exactly one
target is configured, it is selected when `target` is omitted. With multiple
targets, callers must name one. The value must exactly match a configured
target name; an unknown name is an HTTP 400 error. Requests never fall back to
the dashboard's selected or problem target.

Unknown parameters, repeated scalar parameters, and explicitly empty values
are rejected with HTTP 400. Non-GET methods return HTTP 405 with `Allow: GET`.
Query and target errors are client errors; unexpected storage/query failures
return HTTP 500. Error bodies are plain text and should not be parsed as a
versioned response shape.

Set values used in the examples below:

```sh
BASE_URL=http://127.0.0.1:9090
TARGET=my-app
```

## `GET /api/v1/status`

Returns the current collection state and the latest available authoritative
application and dependency health for the selected target.

| Field | Meaning |
|---|---|
| `target` | Configured target name. |
| `type` | Canonical integration type, such as `spring`, `quarkus`, or `statlite-metrics`. |
| `collection_status` | `not_polled` before any poll, `ok` after a successful latest poll, or `error` when the latest poll failed. |
| `last_poll_at` | Completion time of the latest collection attempt, or `null`. |
| `last_successful_poll_at` | Completion time of the latest successful collection, or `null`. |
| `last_failed_poll_at` | Completion time of the latest failed collection, or `null`. |
| `consecutive_collection_failures` | Number of consecutive failed collection attempts. |
| `last_collection_error` | Latest collection error summary, or `null`. Transport diagnostics containing an absolute URL are generalized to `collection request failed`. |
| `application_health` | Application health string reported by the integration, or `null` when unavailable. |
| `dependency_health` | Dependency or database health string reported by the integration, or `null` when unavailable. |
| `health_observed_at` | Completion time of the poll that supplied the health values, or `null`. |

Application and dependency health are independent source values. StatLite does
not normalize them to a universal status vocabulary: a negative value is
different from `null`, which means no observation is available. If the latest
poll reports either health value, both fields come from that poll and an
unreported value is `null`. If the latest poll reports no health, the API may
retain both values from the last successful poll; `health_observed_at` then
shows their older observation time. Collection success is not application
health.

Example:

```sh
curl -fsS --get "$BASE_URL/api/v1/status" \
  --data-urlencode "target=$TARGET" | jq .
```

The same request using only Python's standard library:

```sh
python3 - "$BASE_URL" "$TARGET" <<'PY'
import json
import sys
from urllib.parse import urlencode
from urllib.request import urlopen

base_url, target = sys.argv[1:]
url = base_url.rstrip("/") + "/api/v1/status?" + urlencode({"target": target})
with urlopen(url, timeout=5) as response:
    status = json.load(response)

print(
    f"{status['target']}: collection={status['collection_status']}; "
    f"application={status['application_health'] or 'unreported'}; "
    f"dependency={status['dependency_health'] or 'unreported'}"
)
PY
```

## `GET /api/v1/events`

Returns `{ "target": "...", "events": [...] }`, including an empty array
when no events match. Results are newest first. Event objects contain:

| Field | Meaning |
|---|---|
| `timestamp` | Start time of the poll that recorded the event, not a separate event occurrence time. |
| `severity` | Stored diagnostic severity, currently `warning` or `error`. |
| `type` | Opaque diagnostic type string from the collector or monitor. It is not a normalized, closed, cross-integration category list; avoid depending on specific values as a stable policy interface. |
| `message` | Human-readable diagnostic text. A message containing an absolute HTTP(S) URL is projected to a generic request failure. |
| `metric_key` | Optional existing metric key affected by the diagnostic. Omitted when absent. |

Query parameters:

| Parameter | Behavior |
|---|---|
| `target` | Optional when one target is configured; otherwise required. |
| `range` | `5m`, `1h`, or `24h`; defaults to `1h`. |
| `limit` | Positive integer from 1 to 500; defaults to 100. The limit is applied in the storage query. |

An invalid range or limit returns HTTP 400. Events are subject to the configured
retention window.

Example, showing recent error-severity diagnostics:

```sh
curl -fsS --get "$BASE_URL/api/v1/events" \
  --data-urlencode "target=$TARGET" \
  --data-urlencode range=1h \
  --data-urlencode limit=100 |
  jq -r '.events[] | select(.severity == "error") |
    "\(.timestamp) \(.type): \(.message)"'
```

No output means this response contains no error-severity events. Event type and
message text are diagnostics for people; callers should use status and metrics
fields for machine decisions.

## `GET /api/v1/metrics`

Returns a fixed rolling one-hour view evaluated at request time. It has no
caller-selected range or bucket size. It is an operational summary, not a
general metrics query or a historical analytics endpoint.

The response contains `target`, fixed `range` (`1h`), fixed
`bucket_seconds` (`60`), request-time `evaluated_at`, nullable
`latest_http_observation_at`, and `points`. Points are grouped into occupied
UTC minute slots; empty slots are omitted. Each point's `timestamp` is the
start of the minute containing its source poll time. A point can combine
multiple poll observations.

The complete point fields are:

| Field | Meaning |
|---|---|
| `timestamp` | Start of the occupied UTC minute slot. |
| `requests` | Request-count delta total. |
| `http_4xx` | 4xx response delta total, including 404. |
| `http_5xx` | 5xx response delta total. |
| `http_5xx_rate` | `http_5xx / requests` when requests are positive and every contributing request interval has a usable paired 5xx delta. This is a fraction from 0 to 1 when available. |
| `avg_latency_ms` | Arithmetic mean request latency in milliseconds, weighted by requests whose request and duration counter baselines match. |
| `latency_requests` | Number of requests contributing to `avg_latency_ms`; it may be lower than `requests`. |
| `process_cpu_cores` | Mean of available process CPU gauge samples, in CPU cores. It may exceed 1. |
| `runtime_memory_bytes` | Mean of available runtime-managed memory samples in bytes. Its meaning depends on the integration/runtime; it is not whole-process RSS or a cross-runtime comparable value. |
| `host_cpu_usage` | Mean of available host CPU usage fractions in `[0, 1]`. |
| `host_memory_usage` | Mean of per-poll host memory usage fractions in `[0, 1]`, derived from valid used/total pairs. |
| `host_disk_usage` | Mean of per-poll host disk usage fractions in `[0, 1]`, derived from valid used/total pairs. |

Metrics fields without a usable observation are JSON `null`; zero is a measured
zero. Missing samples, counter resets, process restarts, and unmatched
baselines do not become fabricated zeroes. Incomplete 4xx or 5xx counter
coverage makes the corresponding count `null`. A 5xx rate requires complete
paired 5xx coverage and positive request volume. At zero request volume,
`requests` can be `0` while rate and latency values are `null`.

Latency can cover fewer requests than the bucket's request total. Consumers
should check `latency_requests` before relying on the mean. A slot timestamp
is approximate placement, not a claim that all activity happened during that
minute. When a poll interval spans multiple minutes, including a collection
gap, its whole derived delta is assigned to the end poll's minute; it is not
split or prorated. Poll time is the stored poll start time.

`latest_http_observation_at` is the actual timestamp of the latest source poll
in the hour that contributes a usable HTTP observation. It may advance from
usable requests, latency, or a status delta paired with its request baseline.
It remains `null` for resource-only data and unmatched status-only deltas. It
does not guarantee freshness of every HTTP field: inspect the relevant point's
nullable request, 5xx, rate, and latency-coverage fields, and apply caller-owned
freshness and minimum-volume policy. The minute-slot timestamp can hide up to
a minute of age. A newer resource-only point does not advance HTTP freshness.

Resource measurements are target-scoped. Application-target host values are
included only when that target collected them. For a collocated deployment,
select `statlite-self` separately to inspect the host or container environment
visible to StatLite. Those values are not merged into an application's metrics
response and do not describe a remote application's host. In containers,
resource signals reflect the environment visible to StatLite, which may be the
container rather than the physical host.

The one-hour public read materializes at most 4,096 in-window polls. If that
bound is exceeded, the endpoint returns HTTP 500 instead of silently
truncating the series. Retention is applied as for other stored history.

Example, inspecting the newest point with usable HTTP data and its freshness.
A newer resource-only point may be present in the response:

```sh
curl -fsS --get "$BASE_URL/api/v1/metrics" \
  --data-urlencode "target=$TARGET" |
  jq ' . as $response
    | ([ $response.points[]
         | select(.requests != null or .avg_latency_ms != null
                  or .http_4xx != null or .http_5xx != null) ] | last // null) as $point
    | {
        target,
        evaluated_at,
        latest_http_observation_at,
        latest_http_point: (if $point == null then null else
          ($point | {timestamp, requests, http_4xx, http_5xx, http_5xx_rate,
                     avg_latency_ms, latency_requests})
        end)
      }'
```

Select `statlite-self` explicitly to inspect its host fractions:

```sh
curl -fsS --get "$BASE_URL/api/v1/metrics" \
  --data-urlencode target=statlite-self |
  jq '{target, points: [.points[] | {
    timestamp, host_cpu_usage, host_memory_usage, host_disk_usage
  }]} '
```

Treat absent points and `null` measurements as unavailable. Any thresholds,
minimum traffic, schedules, and notification policy belong to the caller.

## `/healthz` and target status

`/healthz` reports StatLite process and storage readiness, independently of
monitored-target health. It returns a JSON object with `status`, `version`,
`timestamp`, and nested `storage.status`. A SQLite readiness failure produces
HTTP 503 and top-level `status: "error"`; monitored-target poll failures do
not make `/healthz` fail. Use `/api/v1/status` to inspect a target's collection
state and source-reported application/dependency health.

## Contract scope

The API is read-only and intentionally narrow. It adds no authentication,
alert rules, notification delivery, pagination, arbitrary filters, arbitrary
metric queries, or historical series beyond the fixed one-hour metrics view.
The dashboard's unversioned `/api/*` responses are not a compatibility
contract. Future external API versions can be described in this stable
`docs/api.md` reference without changing its URL.
