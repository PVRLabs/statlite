# Traefik service HTTP monitoring

Use `type: traefik` to monitor one HTTP service through Traefik's built-in
Prometheus exporter. The tested setup uses **Traefik 3.8.0**, a single proxy
instance, the file provider, two HTTP backends and a dedicated loopback metrics
listener. StatLite scrapes the exporter directly.

## Configure the metrics listener

Merge these settings into the Traefik static configuration:

```yaml
entryPoints:
  observability:
    address: "127.0.0.1:8082"
metrics:
  prometheus:
    entryPoint: observability
    addServicesLabels: true
```

Keep the listener on loopback for a same-host installation. Send a request
through the monitored Traefik route, then copy the exact `service` label from
the `traefik_service_requests_total` family at the metrics endpoint. The
selector includes the provider suffix, such as `alpha@file`.

Add the target to [statlite.yaml](../configuration.md):

```yaml
targets:
  - name: alpha-via-traefik
    type: traefik
    url: http://127.0.0.1:8082/metrics
    service: alpha@file
```

Several targets can use the same metrics URL with distinct service selectors.
Optional Basic Auth applies to the scrape. Keep production polling at its
normal cadence.

## Inspect the service

```sh
statlite inspect --type traefik --service alpha@file http://127.0.0.1:8082/metrics
```

Typed inspection reports the observed metrics and prints matching YAML. It
reports partial compatibility for a recognizable Traefik endpoint whose
selected service has yet to emit request metrics. The dashboard shows DOWN
after a failed collection until service metrics are observed. This status
reflects collection availability. To confirm the setup, check the exact
selector and per-service metrics, then send a request through the service.

## Read the dashboard

The profile exposes five HTTP signals for the selected service.

| Signal | Verified coverage |
| --- | --- |
| Requests | Completed requests observed at the service |
| 404 | Exact service responses with status 404 |
| 4xx | Service responses with codes 400–499 |
| 5xx | Service responses with codes 500–599, including the observed connection-refused 502 and upstream header timeout 504 |
| Average response time | Service duration measured at Traefik, with matching histogram and request counts |

Scrapes on the dedicated listener contributed zero requests. Unmatched-router
404s and empty-pool 503s appeared outside the service counters in the tested
recipe. UP means StatLite can collect service metrics. Backend health, CPU,
heap, uptime and restart history are unavailable. A direct StatLite integration
can provide application metrics.

The first usable poll establishes a baseline. Proxy restart, a configuration
reload, or a missing series can rebaseline the service. Even unrelated reloads
can leave a gap and reduce Attention coverage. Request counters and duration
histograms are updated separately. A transient mismatch can suppress latency
while request totals remain usable. See the [metric reference](../metrics.md#traefik-service-http-source)
for matching rules, tuple limits, and continuity details.
