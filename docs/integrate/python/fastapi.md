# Integrate FastAPI with StatLite Metrics

This guide adds lightweight FastAPI application monitoring using the fixed
`statlite-metrics/v1` JSON profile and one small, dependency-light helper. A
[runnable and tested demo](../../../examples/python-fastapi-demo/) accompanies
the guide.

## When to use this integration

FastAPI does not have a first-class StatLite target type. Use this direct v1
integration when StatLite's fixed traffic, error, average-latency, status, and
process CPU signals fit the application's operational needs. This in-memory
helper is a single-process or single-worker integration. Multiple Uvicorn or
Gunicorn workers need application-owned shared aggregation or stable
per-worker routing.

The helper intentionally does not emit `started_at` or `uptime_seconds`.
Helper initialization time and lifetime are not necessarily process start time
and process uptime. Add those optional restart-identity signals only when the
application can provide accurate process-level values.

StatLite cannot determine request counts, HTTP errors, or request latency from
outside the application. The middleware below measures those values where
requests and responses pass through FastAPI.

The example was exercised with Python 3.14.6, FastAPI 0.140.7, and Uvicorn
0.51.0. It uses FastAPI's documented HTTP middleware and route APIs, but this
guide claims only that tested baseline rather than compatibility with every
supported FastAPI release.

## Minimal dependency-light integration

Save this complete helper as `statlite_metrics.py`:

```python
"""Small, copyable StatLite Metrics v1 helper for a one-process FastAPI app."""

# Source and updates: https://github.com/PVRLabs/statlite/blob/main/docs/integrate/python/fastapi.md

from __future__ import annotations

import math
import threading
import time
from typing import Any, Awaitable, Callable


SCHEMA = "statlite-metrics/v1"
DEFAULT_METRICS_PATH = "/statlite/metrics"


class StatLiteMetrics:
    """Collect the fixed StatLite Metrics v1 profile in process memory.

    Counters are local to this object, so run one application process (for
    example, one Uvicorn worker) when using this simple implementation.
    """

    def __init__(self, metrics_path: str = DEFAULT_METRICS_PATH) -> None:
        self.metrics_path = metrics_path
        self._last_cpu_wall = time.monotonic()
        self._last_cpu_time = time.process_time()
        self._lock = threading.Lock()

        self._requests_total = 0
        self._responses_404_total = 0
        self._responses_4xx_total = 0
        self._responses_5xx_total = 0
        self._request_duration_seconds_total = 0.0

    async def middleware(
        self,
        request: Any,
        call_next: Callable[[Any], Awaitable[Any]],
    ) -> Any:
        """FastAPI/Starlette HTTP middleware for application request metrics."""
        if request.url.path == self.metrics_path:
            return await call_next(request)

        started = time.perf_counter()
        status_code: int | None = None
        try:
            response = await call_next(request)
            status_code = response.status_code
            return response
        except Exception:
            # FastAPI's outer error middleware will turn this into a 500.
            status_code = 500
            raise
        finally:
            self._record_request(status_code, time.perf_counter() - started)

    def snapshot(self) -> dict[str, Any]:
        """Return one complete, JSON-serializable StatLite Metrics v1 response."""
        with self._lock:
            now = time.monotonic()
            process_cpu_time = time.process_time()
            wall_delta = now - self._last_cpu_wall
            cpu_delta = process_cpu_time - self._last_cpu_time
            process_cpu_usage = cpu_delta / wall_delta if wall_delta > 0 else 0.0
            if not math.isfinite(process_cpu_usage) or process_cpu_usage < 0:
                process_cpu_usage = 0.0
            self._last_cpu_wall = now
            self._last_cpu_time = process_cpu_time

            metrics: dict[str, int | float] = {
                "requests_total": self._requests_total,
                "responses_404_total": self._responses_404_total,
                "responses_4xx_total": self._responses_4xx_total,
                "responses_5xx_total": self._responses_5xx_total,
                "request_duration_seconds_total": (
                    self._request_duration_seconds_total
                ),
                # CPU time consumed during the interval divided by wall time:
                # 1.0 means one logical CPU core was fully used.
                "process_cpu_usage": process_cpu_usage,
            }

        return {
            "schema": SCHEMA,
            "integration": "fastapi",
            "status": "UP",
            "metrics": metrics,
        }

    def _record_request(self, status_code: int | None, duration: float) -> None:
        if not math.isfinite(duration) or duration < 0:
            duration = 0.0

        with self._lock:
            self._requests_total += 1
            self._request_duration_seconds_total += duration

            if status_code == 404:
                self._responses_404_total += 1
            if status_code is not None and 400 <= status_code < 500:
                self._responses_4xx_total += 1
            if status_code is not None and 500 <= status_code < 600:
                self._responses_5xx_total += 1
```

Register the middleware and endpoint in the module that creates the FastAPI
application:

```python
from fastapi import FastAPI

from statlite_metrics import StatLiteMetrics


app = FastAPI()
metrics = StatLiteMetrics()
app.middleware("http")(metrics.middleware)


@app.get("/statlite/metrics")
def statlite_metrics() -> dict:
    return metrics.snapshot()
```

## Existing-library path

No additional metrics-library path is recommended for this integration. A
general instrumentation library would still need an adapter that emits the
fixed v1 JSON fields. StatLite does not ingest arbitrary Prometheus or
OpenMetrics output.

## Endpoint behavior

`GET /statlite/metrics` returns a JSON snapshot with a successful 2xx response.
The middleware excludes that exact path, including requests with a query
string, so StatLite polling does not inflate application traffic or latency.

FastAPI's normal response path exposes final 2xx, 4xx, and framework-generated
404 statuses to the middleware. If an application exception escapes
`call_next`, the exception branch records one 5xx and re-raises it for
FastAPI's error handling. The `finally` block records each application request
exactly once. A 404 increments both `responses_404_total` and
`responses_4xx_total`, as the v1 contract defines 404 as a 4xx subset.
Snapshot collection performs no database or network I/O.

## Required, optional, and status fields

The response always includes the required `schema: "statlite-metrics/v1"` and
a non-empty `status`. Its optional `integration: "fastapi"` identifies the
canonical helper that produced the response. The example's `status: "UP"` is
the application's simple operational assertion at snapshot time. It does not
assert that every dependency is healthy. A successful response proves
reporting availability, not universal application or dependency health.

The helper deliberately omits `database_status`. Add it only when the
application already maintains an authoritative, bounded and cached database
health signal. Read that cached value in `snapshot`; do not query the database
while serving a StatLite poll.

All `metrics` fields and `started_at` are optional under v1. This helper emits
cumulative response metrics and process CPU use in CPU cores. It omits
`started_at` and `uptime_seconds` because a helper's initialization time and
lifetime are not necessarily the process values required by v1. It also omits
runtime heap because Python's standard library does not expose total
interpreter-managed heap without enabling allocation tracing, and StatLite
should not enable that global overhead. Process RSS is not runtime heap. The
v1 compatibility field `request_duration_seconds_max` is also omitted because
StatLite accepts but does not use it. Unsupported host fields are omitted.

## Configure StatLite

Save this as `statlite.yaml`:

```yaml
targets:
  - name: "python-fastapi-app"
    type: "statlite-metrics"
    url: "http://127.0.0.1:8000/statlite/metrics"
```

Application integration is still required. The YAML tells StatLite where to
poll; it does not add the middleware or endpoint to FastAPI.

## Run and verify the integration

Install the tested dependencies and start one Uvicorn worker:

```bash
python3 -m venv .venv
source .venv/bin/activate
python -m pip install 'fastapi==0.140.7' 'uvicorn[standard]==0.51.0'
uvicorn app:app --host 127.0.0.1 --port 8000 --workers 1
```

Leave the application running. In another terminal, generate normal, missing,
and application-error responses:

```bash
curl -s http://127.0.0.1:8000/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8000/missing
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8000/failure
curl -s http://127.0.0.1:8000/statlite/metrics
```

The `/failure` URL is only a verification suggestion; point it at an existing
route that deliberately raises an exception in a safe local environment. The
snapshot should report three completed application requests, one 404 in both
the 404 and 4xx counters, and one 5xx. Verify discovery separately:

```bash
statlite inspect 'http://127.0.0.1:8000/statlite/metrics'
statlite --config statlite.yaml
```

Open <http://127.0.0.1:9091>. Use a 30-second or longer polling interval in
production. See the [runnable demo](../../../examples/python-fastapi-demo/)
for maintained application, configuration, and test files.

## Optional metrics and deployment caveats

This in-memory helper is supported by default only for one application process
or one Uvicorn worker. Multiple workers have separate counters. When
load-balanced polls alternate workers, counters can decrease, producing
misleading deltas rather than merely a partial aggregate. If an application
adds accurate process identity fields, those values can also alternate and
appear to show restarts. StatLite does not aggregate workers. A multi-worker
deployment must provide application-owned shared aggregation or a stable
per-worker endpoint and target topology.

Add host fields only when the application can accurately describe its visible
execution environment. If a reverse proxy mounts the application below a
prefix, ensure the configured URL reaches the endpoint and set
`metrics_path` to the value FastAPI reports as `request.url.path` for it.

The `statlite-metrics` target type does not currently send target credentials,
and `statlite inspect` does not accept authentication options. Make the endpoint
reachable by StatLite through network or proxy access controls that do not
require StatLite to authenticate. Restrict access because the endpoint exposes
operational data.

## References and future first-class support

- [Runnable FastAPI demo](../../../examples/python-fastapi-demo/)
- [StatLite Metrics v1 specification](../../statlite-metrics-v1.md)
- [Why StatLite Metrics?](../../why-statlite-metrics.md)
- [Integration guide index](../)
- [StatLite configuration](../../configuration.md)
- [FastAPI middleware](https://fastapi.tiangolo.com/tutorial/middleware/)

A first-class FastAPI target would be considered only if demand and a stable,
recognizable framework contract justify maintaining it.
