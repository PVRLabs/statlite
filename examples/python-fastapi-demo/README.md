# Integrate FastAPI with StatLite Metrics

This runnable FastAPI application is the canonical FastAPI integration guide
and implementation for StatLite Metrics. It exposes a Hello World route and the
fixed `statlite-metrics/v1` JSON profile.

## When to use this integration

FastAPI does not have a first-class StatLite target type. Use this direct v1
integration when StatLite's fixed traffic, error, average-latency, status,
restart, and process signals fit the application's operational needs.

StatLite polls an endpoint, but it cannot determine request counts, HTTP
errors, or request latency from outside the application. Those values must be
measured where requests and responses pass through FastAPI.

## Minimal dependency-light integration

The [`statlite_metrics.py`](statlite_metrics.py) helper is the single complete,
tested implementation. Copy it into your application. It uses only the Python
standard library, keeps counters in memory, and adds no Prometheus dependency.

Register the helper's middleware and endpoint exactly as shown in
[`app.py`](app.py):

```python
from fastapi import FastAPI
from statlite_metrics import StatLiteMetrics

app = FastAPI()

# Stores request counters and process metrics for this application process.
metrics = StatLiteMetrics()

# Measures application requests. The helper excludes /statlite/metrics.
app.middleware("http")(metrics.middleware)


@app.get("/statlite/metrics")
def statlite_metrics() -> dict:
    return metrics.snapshot()
```

This registration snippet is complete, while the linked helper remains the
one maintained source for its counter, timing, locking, CPU, runtime-memory,
and serialization logic.

## Existing-library path

No additional metrics-library path is recommended for this integration. The
small helper maps FastAPI request behavior directly to the fixed v1 fields.
Adding a general metrics library would not remove the need to emit the v1 JSON
contract, and StatLite does not ingest arbitrary Prometheus or OpenMetrics
output.

## Endpoint behavior

`GET /statlite/metrics` returns a JSON snapshot with a successful 2xx response.
The helper excludes that path from all application request counters and latency
totals, so StatLite polling does not inflate the traffic being monitored. The
snapshot performs no database or network I/O.

The helper emits cumulative request, 404, 4xx, 5xx, and duration metrics. A 404
is included in both `responses_404_total` and `responses_4xx_total`, as the v1
contract defines 404 as a 4xx subset. Unhandled application exceptions are
counted as 5xx when FastAPI lets the middleware observe them.

## Required, optional, and status fields

The response always includes the required `schema: "statlite-metrics/v1"` and
a non-empty `status`. The helper's `status: "UP"` is the simple example
application's operational assertion at snapshot time. It does not assert that
every application dependency is healthy. A successful endpoint response proves
that reporting is available, not universal application or dependency health.

The helper omits `database_status`. Add it only when the application already
maintains an authoritative, inexpensive or cached database-health signal. Do
not query the database just to serve a StatLite poll, and do not invent a value
when no suitable signal exists.

All `metrics` fields and `started_at` are optional under the v1 contract. This
helper emits `started_at`, uptime, request metrics, process CPU usage, and
Python-managed memory when available:

- `process_cpu_usage` is process CPU time divided by wall-clock time since the
  previous snapshot. Its unit is CPU cores, so `1.0` represents one fully used
  logical core and values can exceed `1.0`.
- `runtime_heap_used_bytes`, when present, is the current Python-managed
  allocation size reported by `tracemalloc`. It is not process RSS, a container
  limit, or a maximum heap value.
- `uptime_seconds` and `started_at` describe this application process.

## Configure StatLite

The included [`statlite.yaml`](statlite.yaml) configures the required target:

```yaml
targets:
  - name: "python-fastapi-demo"
    type: "statlite-metrics"
    url: "http://127.0.0.1:8000/statlite/metrics"
```

Application integration is still required. The YAML tells StatLite where to
poll; it does not add the middleware or endpoint to FastAPI.

## Run and verify the demo

From this directory, create an environment and install the two runtime
dependencies:

```bash
python3 -m venv .venv
source .venv/bin/activate
python -m pip install -r requirements.txt
```

Start one Uvicorn worker:

```bash
uvicorn app:app --host 127.0.0.1 --port 8000 --workers 1
```

In another terminal, generate traffic and inspect the profile:

```bash
curl -s http://127.0.0.1:8000/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8000/missing
curl -s http://127.0.0.1:8000/statlite/metrics
```

With an installed StatLite binary, verify that the endpoint is recognized and
review the generated starter configuration:

```bash
statlite inspect 'http://127.0.0.1:8000/statlite/metrics'
```

Then, from the repository's `statlite/` directory with FastAPI still running,
start StatLite using the maintained example configuration:

```bash
go run ./cmd/statlite --config examples/python-fastapi-demo/statlite.yaml
```

Open <http://127.0.0.1:9091>. The demo polls every 10 seconds for responsive
local feedback. Use a 30-second or longer interval in production. Its SQLite
file is created in the current working directory.

## Optional metrics and deployment caveats

The helper intentionally omits optional host CPU, memory, and disk fields. A
central StatLite instance cannot infer resources for a remote host. Emit those
fields deliberately when the application can describe its visible execution
environment accurately, or run StatLite on that host when host visibility is
needed.

This helper is intended for one application process or one Uvicorn worker.
Each worker has separate counters and `started_at`. With multiple workers,
successive polls can reach different workers, making counters decrease and
`started_at` alternate. That produces misleading deltas or apparent restarts,
not merely a partial aggregate. StatLite does not aggregate application
workers. A multi-worker deployment needs application-owned shared aggregation
or a stable per-worker endpoint and target topology.

If a reverse proxy mounts the application below a prefix, ensure the configured
URL reaches the same `/statlite/metrics` path that the helper excludes. Protect
the endpoint with network controls or a trusted proxy when needed. The current
`statlite inspect` workflow does not accept authentication options, and target
authentication is not configured by this example.

## Check the helper

The helper checks use only Python's standard library and do not require the
FastAPI dependencies. From the repository root, run:

```bash
python3 -m unittest examples/python-fastapi-demo/test_statlite_metrics.py
```

## References and future first-class support

- [StatLite Metrics v1 specification](../../docs/statlite-metrics-v1.md)
- [Why StatLite Metrics?](../../docs/why-statlite-metrics.md)
- [Integration guide index](../../docs/integrate/)
- [StatLite configuration](../../docs/configuration.md)

The v1 specification is authoritative for endpoint and field behavior. This
example is the canonical FastAPI helper and test implementation. A separate
first-class FastAPI target would be considered only if demand and a stable,
recognizable framework contract justify maintaining it.
