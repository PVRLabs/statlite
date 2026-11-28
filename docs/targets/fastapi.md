# FastAPI application monitoring

Use `type: fastapi` to monitor a FastAPI application through
`prometheus-fastapi-instrumentator`. The verified setup uses Linux, CPython
3.12.3, FastAPI 0.121.3, Uvicorn 0.38.0, Instrumentator 7.1.0 and the Prometheus
Python client 0.23.1. StatLite scrapes the application directly.

## Expose application metrics

Install `prometheus-fastapi-instrumentator`. Attach the Instrumentator to
your FastAPI application before starting Uvicorn. The following setup enables
exact 404 counts and excludes scrapes at the source.

```python
from prometheus_fastapi_instrumentator import Instrumentator

Instrumentator(
    should_group_status_codes=False,
    excluded_handlers=[r"^/metrics$"],
).instrument(app).expose(app)
```

The default `Instrumentator().instrument(app).expose(app)` setup also works.
Its status labels are grouped, so exact 404 is unavailable. StatLite excludes
the inline scrape handler from request counts and duration in both recipes.

Add the exact metrics endpoint to [statlite.yaml](../configuration.md).

```yaml
targets:
  - name: orders
    type: fastapi
    url: http://127.0.0.1:8000/metrics
```

Use the direct route URL, with its path matching the metrics route's
`handler` label. If a proxy rewrites the path, exclude the internal metrics
route in the exporter and choose an external path that matches no application
`handler` label. StatLite filters the configured path, so a collision would
exclude the matching application traffic. Optional Basic Auth applies to
polling.

## Inspect the endpoint

```sh
statlite inspect --type fastapi http://127.0.0.1:8000/metrics
```

Send a request to an application route before inspecting to see the HTTP
capabilities. Cold endpoints can be recognized with partial compatibility.
Grouped status also reports partial compatibility because exact 404 is
unavailable. Typed inspection prints the observed capabilities and matching
YAML.

## Read the dashboard

| Signal | Default grouped status | Exact status with exporter exclusion |
| --- | --- | --- |
| Requests, 4xx and 5xx | Available | Available |
| Exact 404 | Unavailable | Available |
| Average response time | Available when histogram counts match | Available when histogram counts match |
| Process CPU and restart detection | Available with Linux process metrics | Available with Linux process metrics |
| Runtime heap | Unavailable | Unavailable |
| Verified coverage | 6 of 8 signals | 7 of 8 signals |

Unknown routes contribute to 4xx. Explicit and unhandled 500 responses
contribute once each to requests and 5xx. CPU is measured in logical cores
and requires two observations in the same process lifetime. A restart
establishes a new HTTP baseline. UP reports successful collection.
Application health is unavailable.

See the [metric reference](../metrics.md#fastapi-http-and-process-metrics)
for latency matching, tuple limits and continuity.

## Two Uvicorn workers with a shared registry

Use the exact-status Instrumentator setup above, including the
`/metrics` exclusion. Start both workers with the same
`PROMETHEUS_MULTIPROC_DIR` environment variable set before Python imports
the Prometheus client. Instrumentator's `.expose(app)` then serves the same
deployment-wide registry from either worker. Use the same target YAML and
typed inspection command.

```sh
export PROMETHEUS_MULTIPROC_DIR="$(mktemp -d)"
uvicorn myapp:app --host 127.0.0.1 --port 8000 --workers 2
```

Create a fresh directory for each full deployment start. Keep the directory
and its counter and histogram files across worker replacement so the totals
retain requests handled by the departed worker. Clean up the old directory
after the full deployment stops. The verified setup uses two Uvicorn workers
on one host with a directory dedicated to that deployment. Ordinary
worker-local metrics on a shared port are outside this recipe.

The shared registry provides requests, exact 404, 4xx, 5xx, and average
response time when duration counts match, for five of eight dashboard signals. Attention
uses the aggregate request and error deltas. Process CPU, runtime heap, and
restart identity are unavailable. An observed counter drop starts a fresh
baseline. A full deployment restart can go undetected if the new counters
regrow past their previous values between polls. The first positive value of
a new method, handler, or status tuple also establishes a fresh baseline when
process identity is unavailable.
