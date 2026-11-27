# Flask application monitoring

Use `type: flask` to monitor one Flask application process through
`prometheus-flask-exporter`. The verified setup uses Linux, CPython 3.12.3,
Flask 3.1.2, prometheus-flask-exporter 0.23.2, and the Prometheus Python
client 0.23.1.

## Instrument the application

Install `prometheus-flask-exporter` in the application's Python environment.
Initialize the exporter with its default options when creating the Flask app.

```python
from flask import Flask
from prometheus_flask_exporter import PrometheusMetrics

app = Flask(__name__)
metrics = PrometheusMetrics(app)
```

Use one serving process. The exporter serves `/metrics` on the application
listener and excludes its own scrapes from the default HTTP metrics.

## Configure and inspect the target

```yaml
targets:
  - name: orders
    type: flask
    url: http://127.0.0.1:8000/metrics
```

Use the metrics URL reachable from StatLite. For a protected endpoint,
configure polling credentials in the [Basic Auth block](../configuration.md#basic-auth).

```sh
statlite inspect --type flask http://127.0.0.1:8000/metrics
```

Cold inspection reports partial compatibility until an application request
initializes the HTTP counters. Inspection then shows the available HTTP
and process capabilities.

## Read the dashboard

| Signal | Availability |
| --- | --- |
| Requests, exact 404, 4xx, and 5xx | From completed response statuses |
| Average response time | When the duration histogram matches the completed responses |
| Process CPU and restart detection | With compatible process metrics, verified on Linux |
| Runtime heap | Unavailable |
| Verified coverage | 7 of 8 signals |

Unmatched URLs count toward exact 404 and 4xx. Explicit and unhandled 500s
each count once toward requests and 5xx. CPU is measured in logical cores
between observations of the same process. A restart establishes a fresh
baseline. UP indicates that StatLite collected metrics successfully.
Application health is unavailable.

The default histogram creates a series for each method, concrete URL path,
and status. For example, requests to `/orders/12` and `/orders/13` use
different series even when they share one Flask route. StatLite tracks at
most 256 duration tuples. After the limit is exceeded, valid request and
error counts continue while latency is unavailable. A process restart
clears the exporter's accumulated series and allows latency to resume
once compatible observations are collected. See the
[Flask metric reference](../metrics.md#flask-http-and-process-metrics) for
count and continuity details.

This profile covers the exporter's default metrics from one process.
Multi-worker aggregation and alternate histogram grouping are outside the
verified setup.
