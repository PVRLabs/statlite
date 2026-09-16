# Integrate Django with StatLite Metrics

This guide adds lightweight Django application monitoring using the fixed
`statlite-metrics/v1` JSON profile and one small, dependency-light helper.

## When to use this integration

Django does not have a first-class StatLite target type. Use this direct v1
integration when StatLite's fixed traffic, error, average-latency, status,
and process CPU signals fit the application's operational needs. This
dependency-light example is a single-worker integration. A single Django
worker can be a reasonable choice for a small VPS or an application beginning
to receive traffic, but common Gunicorn, uWSGI, and other multi-worker
deployments need application-owned shared aggregation or stable per-worker
routing.

The helper intentionally does not emit `started_at` or `uptime_seconds`:
helper initialization time and lifetime are not necessarily process start time
and process uptime. Add those optional restart-identity signals only when the
application can provide accurate process-level values.

StatLite cannot determine request counts, HTTP errors, or request latency from
outside the application. The middleware below measures those values where
requests and responses pass through Django.

The example was exercised with Python 3.14.6 and Django 5.2.17 LTS. It uses the
documented synchronous middleware and response APIs, but this guide claims only
that tested baseline rather than compatibility with every supported Django
release or an asynchronous middleware stack.

## Minimal dependency-light integration

Save this complete helper as `statlite_metrics.py` in a Django application
package:

```python
import threading
import time

from django.http import JsonResponse
from django.views.decorators.http import require_GET


METRICS_PATH = "/statlite/metrics"


class StatLiteMetrics:
    def __init__(self):
        self._lock = threading.Lock()
        self._previous_cpu = time.process_time()
        self._previous_cpu_time = time.monotonic()
        self._requests = 0
        self._responses_404 = 0
        self._responses_4xx = 0
        self._responses_5xx = 0
        self._duration_seconds = 0.0

    def record(self, status_code, duration_seconds):
        with self._lock:
            self._requests += 1
            self._duration_seconds += duration_seconds
            if status_code == 404:
                self._responses_404 += 1
            if 400 <= status_code < 500:
                self._responses_4xx += 1
            if 500 <= status_code < 600:
                self._responses_5xx += 1

    def snapshot(self):
        with self._lock:
            now = time.monotonic()
            cpu = time.process_time()
            elapsed = now - self._previous_cpu_time
            process_cpu_usage = (
                (cpu - self._previous_cpu) / elapsed if elapsed > 0 else 0.0
            )
            self._previous_cpu = cpu
            self._previous_cpu_time = now
            metrics = {
                "requests_total": self._requests,
                "responses_404_total": self._responses_404,
                "responses_4xx_total": self._responses_4xx,
                "responses_5xx_total": self._responses_5xx,
                "request_duration_seconds_total": self._duration_seconds,
                "process_cpu_usage": process_cpu_usage,
            }

        return {
            "schema": "statlite-metrics/v1",
            "status": "UP",
            "metrics": metrics,
        }


metrics = StatLiteMetrics()


class StatLiteMetricsMiddleware:
    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        if request.path == METRICS_PATH:
            return self.get_response(request)

        started = time.monotonic()
        try:
            response = self.get_response(request)
        except Exception:
            metrics.record(500, time.monotonic() - started)
            raise

        metrics.record(response.status_code, time.monotonic() - started)
        return response


@require_GET
def statlite_metrics_view(request):
    return JsonResponse(metrics.snapshot())
```

Put the middleware near the beginning of `MIDDLEWARE` so it observes final
responses from the application and middleware below it:

```python
MIDDLEWARE = [
    "django.middleware.security.SecurityMiddleware",
    "myapp.statlite_metrics.StatLiteMetricsMiddleware",
    "django.contrib.sessions.middleware.SessionMiddleware",
    "django.middleware.common.CommonMiddleware",
    "django.middleware.csrf.CsrfViewMiddleware",
    "django.contrib.auth.middleware.AuthenticationMiddleware",
    "django.contrib.messages.middleware.MessageMiddleware",
]
```

Register the exact endpoint in the project's `urls.py`:

```python
from django.contrib import admin
from django.urls import path

from myapp.statlite_metrics import statlite_metrics_view

urlpatterns = [
    path("admin/", admin.site.urls),
    path("statlite/metrics", statlite_metrics_view),
]
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

Django converts exceptions raised by lower layers into responses before
returning them through outer middleware, so the normal response path observes
their final status. The `except` branch records a 5xx only when an exception
actually escapes `get_response`; the success branch cannot then run, which
prevents double-counting. Put custom exception-to-response middleware below
this middleware so its final status is visible here.

A 404 increments both `responses_404_total` and `responses_4xx_total`, as the
v1 contract defines 404 as a 4xx subset. Snapshot collection performs no
database or network I/O.

## Required, optional, and status fields

The response always includes the required `schema: "statlite-metrics/v1"` and
a non-empty `status`. The example's `status: "UP"` is the application's simple
operational assertion at snapshot time. It does not assert that every
dependency is healthy. A successful response proves reporting availability,
not universal application or dependency health.

The helper deliberately omits `database_status`. Add it only when the
application already maintains an authoritative, bounded and cached database
health signal. Read that cached value in `snapshot`; do not call
`connection.ensure_connection()`, execute a query, or otherwise contact the
database while serving a StatLite poll.

All `metrics` fields and `started_at` are optional under v1. This helper emits
cumulative response metrics and process CPU use in CPU cores. It omits
`started_at` and `uptime_seconds` because a helper's initialization time and
lifetime are not necessarily the process start time and process uptime required
by v1. It also omits runtime heap because Python's standard library does not
expose total interpreter-managed heap without enabling extra tracking, and
process RSS is not runtime heap. Unsupported host CPU, memory, and disk fields
are also omitted.

## Configure StatLite

Save this as `statlite.yaml`:

```yaml
targets:
  - name: "python-django-app"
    type: "statlite-metrics"
    url: "http://127.0.0.1:8000/statlite/metrics"
```

Application integration is still required. The YAML tells StatLite where to
poll; it does not add the middleware or endpoint to Django.

## Run and verify the integration

Install the tested Django version and start one development-server process:

```bash
python3 -m venv .venv
source .venv/bin/activate
python -m pip install 'Django==5.2.17'
python manage.py runserver 127.0.0.1:8000
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
view that deliberately raises an exception in a safe local environment. The
snapshot should reflect the completed application requests and their final
statuses. Verify discovery separately:

```bash
statlite inspect 'http://127.0.0.1:8000/statlite/metrics'
statlite --config statlite.yaml
```

Open <http://127.0.0.1:9091>. Use a 30-second or longer polling interval in
production.

## Optional metrics and deployment caveats

This in-memory helper is supported by default only for a single Django worker.
Gunicorn, uWSGI, and other multi-worker servers give each worker separate
counters. When load-balanced polls alternate workers, counters can decrease,
producing misleading deltas rather than merely a partial aggregate. If an
application extends the response with an accurate process `started_at`, that
value can also alternate and appear to show restarts. StatLite does not
aggregate workers. A multi-worker deployment must provide application-owned
shared aggregation or a stable per-worker endpoint and target topology.

The example is synchronous. An application with an asynchronous middleware
stack should implement and test an async-capable equivalent rather than rely on
Django's sync-to-async adaptation for a frequently executed metrics helper.

Add host fields only when the application can accurately describe its visible
execution environment. If a reverse proxy or `FORCE_SCRIPT_NAME` mounts the
application below a prefix, ensure the configured URL reaches the endpoint and
set `METRICS_PATH` to the value Django reports as `request.path` for it. Avoid
redirects caused by adding a trailing slash.

The `statlite-metrics` target type does not currently send target credentials,
and `statlite inspect` does not accept authentication options. Make the endpoint
reachable by StatLite through network or proxy access controls that do not
require StatLite to authenticate. Do not place a Basic Auth or other credential
challenge between StatLite and this endpoint. Restrict access because the
endpoint exposes operational data.

## References and future first-class support

- [StatLite Metrics v1 specification](../../statlite-metrics-v1.md)
- [Why StatLite Metrics?](../../why-statlite-metrics.md)
- [Integration guide index](../)
- [StatLite configuration](../../configuration.md)
- [Django middleware](https://docs.djangoproject.com/en/5.2/topics/http/middleware/)
- [Django request and response objects](https://docs.djangoproject.com/en/5.2/ref/request-response/)

A first-class Django target would be considered only if demand and a stable,
recognizable framework contract justify maintaining it.
