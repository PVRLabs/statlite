# Integrate Express with StatLite Metrics

This guide adds lightweight Node.js/Express application monitoring using the
fixed `statlite-metrics/v1` JSON profile and one small, dependency-light
helper.

A [runnable and tested demo](../../../examples/node-express-demo/) accompanies
the guide.

## When to use this integration

Express does not have a first-class StatLite target type. Use this direct v1
integration when StatLite's fixed traffic, error, average-latency, status,
restart, and process signals fit the application's operational needs. This
dependency-light example is a single-process integration. Cluster mode and
process managers need application-owned shared aggregation or stable
per-worker routing.

StatLite cannot determine request counts, HTTP errors, or request latency from
outside the application. The middleware below measures those values where
requests and responses pass through Express.

The example was exercised with Node.js 24.21.0 LTS and Express 5.1.0. It uses stable
Node.js process APIs and the standard Express middleware and response APIs, but
this guide claims only that tested baseline rather than compatibility with
every supported Express release.

## Minimal dependency-light integration

Save this complete helper as `statlite-metrics.js`:

```js
// Source and updates: https://github.com/PVRLabs/statlite/blob/main/docs/integrate/node/express.md

const { performance } = require("node:perf_hooks");

const METRICS_PATH = "/statlite/metrics";
const startedAt = new Date(Date.now() - process.uptime() * 1000);

const counters = {
  requestsTotal: 0,
  responses404Total: 0,
  responses4xxTotal: 0,
  responses5xxTotal: 0,
  requestDurationSecondsTotal: 0,
};

let previousCpu = process.cpuUsage();
let previousCpuTime = performance.now();

function record(statusCode, durationSeconds) {
  counters.requestsTotal += 1;
  counters.requestDurationSecondsTotal += durationSeconds;
  if (statusCode === 404) counters.responses404Total += 1;
  if (statusCode >= 400 && statusCode < 500) counters.responses4xxTotal += 1;
  if (statusCode >= 500 && statusCode < 600) counters.responses5xxTotal += 1;
}

function statliteMetricsMiddleware(req, res, next) {
  if (req.path === METRICS_PATH) return next();

  const requestStarted = performance.now();
  res.once("finish", () => {
    record(res.statusCode, (performance.now() - requestStarted) / 1000);
  });
  next();
}

function snapshot() {
  const now = performance.now();
  const cpu = process.cpuUsage();
  const elapsedSeconds = (now - previousCpuTime) / 1000;
  const cpuSeconds =
    (cpu.user - previousCpu.user + cpu.system - previousCpu.system) / 1e6;
  const processCpuUsage = elapsedSeconds > 0 ? cpuSeconds / elapsedSeconds : 0;
  previousCpu = cpu;
  previousCpuTime = now;

  return {
    schema: "statlite-metrics/v1",
    integration: "express",
    status: "UP",
    started_at: startedAt.toISOString(),
    metrics: {
      requests_total: counters.requestsTotal,
      responses_404_total: counters.responses404Total,
      responses_4xx_total: counters.responses4xxTotal,
      responses_5xx_total: counters.responses5xxTotal,
      request_duration_seconds_total: counters.requestDurationSecondsTotal,
      process_cpu_usage: processCpuUsage,
      runtime_heap_used_bytes: process.memoryUsage().heapUsed,
      uptime_seconds: process.uptime(),
    },
  };
}

function statliteMetricsEndpoint(req, res) {
  res.json(snapshot());
}

module.exports = {
  METRICS_PATH,
  statliteMetricsEndpoint,
  statliteMetricsMiddleware,
};
```

Register it before application routes so every application response is
observed. Keep Express's error handler after the routes:

```js
const express = require("express");
const {
  METRICS_PATH,
  statliteMetricsEndpoint,
  statliteMetricsMiddleware,
} = require("./statlite-metrics");

const app = express();

app.use(statliteMetricsMiddleware);
app.get(METRICS_PATH, statliteMetricsEndpoint);

app.get("/", (req, res) => res.json({ message: "hello" }));
app.get("/failure", (req, res, next) => next(new Error("example failure")));

app.use((err, req, res, next) => {
  console.error(err);
  if (res.headersSent) return next(err);
  res.status(500).json({ error: "internal server error" });
});

app.listen(3000, "127.0.0.1");
```

## Existing-library path

No additional metrics-library path is recommended for this integration. A
general instrumentation library would still need an adapter that emits the
fixed v1 JSON fields. StatLite does not ingest arbitrary Prometheus or
OpenMetrics output.

## Endpoint behavior

`GET /statlite/metrics` returns a JSON snapshot with a successful 2xx response.
The middleware excludes that path, including requests with a query string, so
StatLite polling does not inflate application traffic or latency.

The `finish` listener runs once after Express and any later error middleware
choose the final response status. It therefore counts completed normal, 404,
4xx, and 5xx responses without double-counting an error passed through
`next(err)`. Connections that close before a response finishes are not counted
as completed responses.

A 404 increments both `responses_404_total` and `responses_4xx_total`, as the
v1 contract defines 404 as a 4xx subset. Snapshot collection performs no
database or network I/O.

## Required, optional, and status fields

The response always includes the required `schema: "statlite-metrics/v1"` and
a non-empty `status`. Its optional `integration: "express"` identifies the
canonical helper that produced the response. The example's `status: "UP"` is
the application's simple operational assertion at snapshot time. It does not
assert that every dependency is healthy. A successful response proves
reporting availability, not universal application or dependency health.

The helper omits `database_status`. Add it only from an authoritative,
inexpensive or cached application signal. Do not query the database merely to
serve a StatLite poll, and do not invent a value when no suitable signal
exists.

All `metrics` fields and `started_at` are optional under v1. This helper emits
cumulative response metrics, process CPU use in CPU cores, V8 heap use in
bytes, process uptime, and an estimated process start time. V8 `heapUsed` is
runtime-managed heap, not process RSS, container memory, or a maximum heap
value. Unsupported host CPU, memory, and disk fields are omitted.

## Configure StatLite

Save this as `statlite.yaml`:

```yaml
targets:
  - name: "node-express-app"
    type: "statlite-metrics"
    url: "http://127.0.0.1:3000/statlite/metrics"
```

Application integration is still required. The YAML tells StatLite where to
poll; it does not add the middleware or endpoint to Express.

## Run and verify the integration

Install Express and start the application:

```bash
npm install express@5.1.0
node app.js
```

Leave the application running. In another terminal, generate normal, missing,
and error responses:

```bash
curl -s http://127.0.0.1:3000/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3000/missing
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3000/failure
curl -s http://127.0.0.1:3000/statlite/metrics
```

The snapshot should report three completed application requests, one 404 in
both the 404 and 4xx counters, and one 5xx. Verify discovery separately:

```bash
statlite inspect 'http://127.0.0.1:3000/statlite/metrics'
statlite --config statlite.yaml
```

Open <http://127.0.0.1:9090>. Use a 30-second or longer polling interval in
production.

## Optional metrics and deployment caveats

This in-memory helper is supported by default only for a single Node.js
process. Cluster mode and process managers give each worker separate counters
and a separate `started_at`. When load-balanced polls alternate workers,
counters can decrease and `started_at` can change, producing misleading deltas
or apparent restarts rather than merely a partial aggregate. StatLite does not
aggregate workers. A multi-process deployment must provide application-owned
shared aggregation or a stable per-worker endpoint and target topology.

Add host fields only when the application can accurately describe its visible
execution environment. Do not substitute process RSS for
`runtime_heap_used_bytes`.

If a reverse proxy mounts the application below a prefix, ensure the configured
URL reaches the same `/statlite/metrics` path that the middleware excludes.
The `statlite-metrics` target type does not currently send target credentials,
and `statlite inspect` does not accept authentication options. Make the endpoint
reachable by StatLite through network or proxy access controls that do not
require StatLite to authenticate. Do not place a Basic Auth or other credential
challenge between StatLite and this endpoint. Restrict access because the
endpoint exposes operational data.

## References and future first-class support

- [Runnable Express demo](../../../examples/node-express-demo/)
- [StatLite Metrics v1 specification](../../statlite-metrics-v1.md)
- [Why StatLite Metrics?](../../monitoring-options.md#why-statlite-metrics)
- [Integration guide index](../)
- [StatLite configuration](../../configuration.md)
- [Express middleware](https://expressjs.com/en/guide/using-middleware.html)
- [Node.js process APIs](https://nodejs.org/api/process.html)

A first-class Express target would be considered only if demand and a stable,
recognizable framework contract justify maintaining it.
