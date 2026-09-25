# External API automation recipes

These `/bin/sh` scripts make one read-only check through StatLite's
[external API v1](../../docs/api.md), print a result, and exit. They require
`curl` and `jq`. Exit status `0` means OK, `1` means the configured condition
triggered, and `2` means the script could not evaluate the condition, including
missing or stale data and API errors. A caller can route those outcomes to its
own notification system.

## Common checks

| Goal | Script | When it alerts |
|---|---|---|
| Catch collection or reported health failures | `check-status.sh` | The latest collection failed, or the source reported a configured unhealthy value. |
| Catch 5xx spikes | `check-http-5xx.sh` | The recent 5xx rate exceeds your threshold after enough requests. |
| Catch host pressure | `check-host-resources.sh` | Host memory or disk usage exceeds its configured fraction. |
| Catch sustained process CPU | `check-sustained-cpu.sh` | Process CPU exceeds a core threshold in consecutive minute points. |

## Run a check

Start StatLite with a configured target, then run a script from the repository
root. Set `BASE_URL` and `TARGET` for your instance. The examples below use
illustrative caller policy values. They are not StatLite defaults or
recommendations.

**Collection and health:** catch a failed collection or a reported unhealthy
application or dependency value. The script checks exact source health strings
configured in `UNHEALTHY_VALUES`.

```sh
BASE_URL=http://127.0.0.1:9090 TARGET=my-app \
  ./examples/api-automation/check-status.sh
```

Possible output, with exit status 0, 1, or 2 respectively:

```text
OK: collection and configured health checks; application=UP dependency=UP
ALERT: latest collection failed (consecutive failures: 2)
UNKNOWN: application health is unreported
```

**Recent 5xx spike:** require at least 20 requests in the latest usable HTTP
minute point and trigger if more than 5% were 5xx responses.

```sh
BASE_URL=http://127.0.0.1:9090 TARGET=my-app \
  MIN_REQUESTS=20 MAX_5XX_RATE=0.05 MAX_HTTP_AGE_SECONDS=180 \
  ./examples/api-automation/check-http-5xx.sh
```

Possible output, with exit status 0, 1, or 2 respectively:

```text
OK: 5xx rate 0.01 across 120 requests
ALERT: 5xx rate 0.08 exceeds 0.05 across 120 requests
UNKNOWN: recent HTTP point has insufficient requests
```

**Host memory or disk pressure:** trigger when either usage fraction exceeds
90%. In a collocated deployment, `statlite-self` observes the environment
visible to StatLite.

```sh
BASE_URL=http://127.0.0.1:9090 TARGET=statlite-self \
  MAX_MEMORY_USAGE=0.90 MAX_DISK_USAGE=0.90 \
  ./examples/api-automation/check-host-resources.sh
```

Possible output, with exit status 0, 1, or 2 respectively:

```text
OK: host memory=0.78 disk=0.64
ALERT: host memory=0.95 disk=0.64
UNKNOWN: no host memory or disk observation
```

**Sustained process CPU:** trigger when process CPU exceeds 0.8 cores in each
of 3 consecutive UTC minute points. `0.80` means 0.8 CPU cores, not 80% host
CPU usage.

```sh
BASE_URL=http://127.0.0.1:9090 TARGET=my-app \
  CPU_CORES_THRESHOLD=0.80 CONSECUTIVE_MINUTES=3 \
  ./examples/api-automation/check-sustained-cpu.sh
```

Possible output, with exit status 0, 1, or 2 respectively:

```text
OK: process CPU did not exceed 0.8 cores in all 3 consecutive minute points
ALERT: process CPU exceeded 0.8 cores in 3 consecutive minute points
UNKNOWN: occupied minute points have a gap
```

## How the checks handle missing data

The status check requires a recent successful collection and reported
application health. Configure `UNHEALTHY_VALUES` as a comma-separated list of
exact strings your integration uses, for example `DOWN,OUT_OF_SERVICE`. Health
strings are integration-defined. A configured unhealthy value or a recent
failed collection triggers an alert. Missing or stale application health
produces an unknown result. Dependency health is checked when reported; an
unreported dependency is shown as such and is never described as healthy.
`MAX_POLL_AGE_SECONDS` and
`MAX_HEALTH_AGE_SECONDS` control its freshness limits.

The 5xx check uses the latest usable HTTP minute point and the response's
`latest_http_observation_at` for freshness. It requires at least
`MIN_REQUESTS` in that point and a non-null `http_5xx_rate`. A missing or null
rate, too little traffic, or a stale HTTP observation returns unknown. The
rate threshold is a fraction; `0.05` means 5%.

The host check evaluates the latest point with host memory or disk data. It
alerts if either available fraction exceeds its threshold. It returns unknown
if neither is available or if a missing field prevents an all-clear result.
The default target, `statlite-self`, reports the host or container environment
visible to StatLite in a collocated deployment. You can select another target
that reports its own host fields, as in a scripted fixture. StatLite does not
merge self-monitoring data into application targets, and these values do not
describe a remote application's host.

The CPU check requires `CONSECUTIVE_MINUTES` occupied, consecutive UTC minute
points with non-null `process_cpu_cores`. It triggers only when every point
exceeds the configured core threshold. Missing minute slots or CPU values
return unknown. The newest point must be within `MAX_POINT_AGE_SECONDS`; this
age uses its minute-start timestamp, which can be up to one minute earlier
than the poll that supplied the value. The host check uses the same age rule.
These are process CPU cores, not host CPU usage fractions.

Each invocation performs one check and exits. Use cron, a systemd timer, or an
existing scheduler to run it repeatedly. For a simple local experiment:

```sh
while true; do
  TARGET=my-app ./examples/api-automation/check-status.sh
  sleep 60
done
```

The caller owns scheduling, deduplication, notification delivery, and any
response to exit status `2`. The scripts keep no alert state.

You can use exit status `1` to send a message through a service you already
use, such as an ntfy topic, Slack or Discord webhook, Telegram bot, or another
HTTP notifier. Keep exit status `2` separate so missing data or an API error
does not become a condition alert. For example, replace the `printf` actions
with your own notification and check-failure handling:

```sh
if TARGET=my-app ./examples/api-automation/check-http-5xx.sh; then
  : # OK
else
  case $? in
    1) printf '%s\n' 'send a 5xx alert through your notifier' ;;
    2) printf '%s\n' 'report that the check could not be evaluated' ;;
  esac
fi
```
