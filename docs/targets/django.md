# Django application monitoring

Use `type: django` to monitor one Django application process through
`django-prometheus`. StatLite scrapes the exporter directly. The verified
setup uses Linux, CPython 3.12.3, Django 5.2.8, django-prometheus 2.4.1,
and the Prometheus Python client 0.23.1.

## Add the Django middleware

Install `django-prometheus` and add `"django_prometheus"` to
`INSTALLED_APPS`. Put
`"django_prometheus.middleware.PrometheusBeforeMiddleware"` first in
`MIDDLEWARE` and
`"django_prometheus.middleware.PrometheusAfterMiddleware"` last.

Choose one way to expose metrics.

## Inline endpoint

Include the exporter URLs at the root of your Django URL configuration.

```python
from django.urls import include, path

urlpatterns += [path("", include("django_prometheus.urls"))]
```

Use the standard `prometheus-django-metrics` view name without a URL
namespace. StatLite uses that name to exclude scrapes from requests and
errors. The outer histogram includes inline scrapes, so average response
time is unavailable.

```yaml
targets:
  - name: orders
    type: django
    url: http://127.0.0.1:8000/metrics
    django_metrics_mode: inline
```

## Separate listener

For average response time, omit the Django exporter URL include and start
the Prometheus client listener once in the process that serves Django.
For example, add this after creating the application in a single-process
WSGI entry point.

```python
from django.core.wsgi import get_wsgi_application
from prometheus_client import start_http_server

application = get_wsgi_application()
start_http_server(8001, addr="127.0.0.1")
```

Use the same default registry as the Django middleware. The listener
bypasses Django's request pipeline. The loopback example assumes StatLite
can reach the listener on the same host.

```yaml
targets:
  - name: orders
    type: django
    url: http://127.0.0.1:8001/metrics
    django_metrics_mode: separate
```

## Inspect the endpoint

```sh
statlite inspect --type django --django-metrics-mode inline http://127.0.0.1:8000/metrics
statlite inspect --type django --django-metrics-mode separate http://127.0.0.1:8001/metrics
```

Send a request to an application route before inspecting to see the HTTP
capabilities. Cold endpoints can report partial compatibility. The two
recipes share metric families, so select the mode explicitly. Inspection
rejects separate mode if the inline metrics view has been observed. For
protected endpoints, configure polling credentials in the
[Basic Auth block](../configuration.md#basic-auth).

## Read the dashboard

| Signal | Inline | Separate listener |
| --- | --- | --- |
| Requests, exact 404, 4xx, and 5xx | Available | Available |
| Average response time | Unavailable | Available when histogram counts match completed responses |
| Process CPU and restart detection | Available with Linux process metrics | Available with Linux process metrics |
| Runtime heap | Unavailable | Unavailable |
| Verified coverage | 6 of 8 signals | 7 of 8 signals |

Unknown URLs count toward exact 404 and 4xx. Explicit and unhandled 500
responses count once each toward requests and 5xx. CPU is measured in
logical cores between observations of the same process. A restart
establishes a fresh HTTP baseline. UP reports successful metric collection.
Application health is unavailable.

These recipes were verified with one WSGI process and the two exporter
middleware components. Middleware that short-circuits the response can
change which requests reach the inner response counters. StatLite leaves
latency unavailable when the outer count and completed-response count
diverge. The per-view histogram can observe unhandled exceptions twice
and is excluded from application latency. See the
[metric reference](../metrics.md#django-http-and-process-metrics) for
continuity and tuple limits.
