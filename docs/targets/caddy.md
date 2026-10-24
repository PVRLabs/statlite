# Caddy HTTP monitoring

Monitor one application's HTTP traffic through Caddy with `type: caddy`, an
exact metrics `url`, and an exact `host`. No application instrumentation is
required, and the backend language does not matter. Support is limited to the
built-in metrics of **Caddy 2.11.4**, with `per_host`, an explicit named HTTPS
site, and one direct `reverse_proxy`.

## Same VPS

Caddy allows one global options block. If your Caddyfile already has one, add
`metrics { per_host }` inside it. A new file can start with:

```caddyfile
{
    metrics {
        per_host
    }
}

app.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Use an ordinary HTTPS hostname with DNS and normal HTTP/HTTPS ports configured
for Caddy. Keep the default writable admin listener on loopback. Send one
request, for example `curl https://app.example.com/`, then copy `host` from the
metrics scrape before writing the StatLite target. `app.example.com` is correct
only when the emitted label is exactly that value:

```bash
curl -fsS http://127.0.0.1:2019/metrics | grep caddy_http_request_duration_seconds_count
```

`host` matches that label exactly, including case and any literal port. A
nonstandard port in the request's Host header changes the label. Do not put a
scheme or path in `host`.

Add the copied value to your [StatLite configuration](../configuration.md):

```yaml
targets:
  - name: app-via-caddy
    type: caddy
    url: http://127.0.0.1:2019/metrics
    host: app.example.com
```

Keep production polling at 30 seconds or longer. Until a request has been
observed, the dashboard shows a failed collection and Recent events lists
`caddy_http_not_observed`, which is not zero traffic. Wait for two usable polls.
The first establishes a baseline; later traffic produces request deltas. Idle
intervals after a baseline show zero requests and unavailable average latency.

Configure one target per host. Different hosts may share the same metrics
endpoint, with independent polls; duplicate endpoint/host selections are
rejected. Endpoint aliases are not discovered. Existing optional Basic Auth
applies to the metrics request.
Caddy targets do not accept health, Spring source, or host-resource fields.
Configure these targets manually; Caddy discovery, typed inspection, and config
writers are not supported.

## Private Docker network

Use a dedicated read-only metrics listener instead of opening the writable admin
listener for scraping. Merge `metrics { per_host }` into the existing global
options block, as in the same-VPS setup. A new file can use:

```caddyfile
{
    metrics {
        per_host
    }
}

app.example.com {
    reverse_proxy app:8080
}

http://:9180 {
    metrics /metrics
}
```

Place Caddy and StatLite on a shared private Docker network. Set
`url: http://caddy:9180/metrics` and keep `host: app.example.com`. Do not publish
9180 publicly. If StatLite runs on the host, publish only
`127.0.0.1:9180:9180` and use `http://127.0.0.1:9180/metrics`. Publish the site's
normal HTTP/HTTPS ports as usual. The metrics listener adds no exporter or
service. Both its scrapes and admin metrics traffic are excluded from application
request charts, as are automatic HTTP-to-HTTPS redirects.

## Reading the dashboard

**UP means StatLite can collect Caddy metrics. It does not guarantee that the
upstream application is healthy.** A collected Caddy-generated 502 can coexist
with UP. DOWN indicates unavailable collection. Application health, dependency
health, Started, application restarts, and application/host resources are
unavailable from this integration.

The existing request, error, and average response time charts show HTTP effects
observed at Caddy. Response time includes work observed at the proxy; it is not
isolated application execution time. A 500 burst raises 5xx counts; slow
successful responses raise average latency without requiring errors; a volume
change alters request counts. Use application logs, traces, and runtime tools
to find the cause. HTTP 200 business/payload failures, internal exceptions,
background jobs, and traffic bypassing Caddy are outside this contract. StatLite
does not inspect response bodies.

Only these existing cumulative counters are stored:

| Caddy duration family observation | Normalized counter |
| --- | --- |
| `_count`, selected host and `handler="subroute"` | `http_requests_total` |
| Same counts with code 404 | `http_404_total` |
| Same counts with codes 400–499 (including 404) | `http_4xx_total` |
| Same counts with codes 500–599 (including 502) | `http_5xx_total` |
| Paired `_sum`, in seconds | `http_request_time_total_seconds` |

Count and sum must match by full label tuple before aggregation, with exactly
one matching server. Average response time is duration delta / request delta,
displayed in milliseconds. No histogram buckets or alternative request family
are collected. See [metric semantics](../metrics.md).

## Diagnostics and limits

- `caddy_http_not_observed`: “HTTP metrics not yet observed.” Generate traffic
  and check the exact host and metrics configuration, including `per_host`.
  Missing metrics are neither zero traffic nor proof of a dead application.
- `metrics_fetch_failed`: transport, parser, or selected-series validation
  failed. Check endpoint access and the event's details, including ambiguous
  servers, invalid labels/values, duplicate tuples, or bounds.
- `caddy_population_invalid`: count/sum pairs or aggregate totals are unusable.
  The entire selected HTTP population is unavailable; there is no fallback
  handler or cross-server aggregation.

At most 256 complete selected tuples are retained. Wildcard/on-demand TLS,
catch-all sites, path-separated apps, and mixed static/API routing are outside
the supported shape. Identical-label traffic cannot be separated.

Raw cumulative counters remain authoritative. First observation, observable
counter decreases, disappearing tuples, server changes, and recovery after
failed collection or persistence establish a fresh anonymous baseline. Gaps
across these boundaries contribute no delta and never claim an application
restart. New complete method/status tuples can join without a break. A reset
that regrows past previous values entirely between polls can be invisible.

These are operational histories, not accounting-grade totals or complete reload
detection. Long-range chart sampling can hide short bursts; averages hide tails
and route mixes. There are no percentiles or per-route/method/code charts.

## Tested versions

The bounded contract uses Caddy **2.11.4**. Local HTTPS acceptance uses a
client-trusted fixture certificate; public automatic certificate issuance was
not exercised. This does not claim arbitrary Caddy 2.x compatibility, a full
Compose distribution, or a Coolify adapter.
