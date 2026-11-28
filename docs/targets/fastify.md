# Fastify application monitoring

Use `type: fastify` to monitor a single Fastify application process. The
verified setup uses Node.js 25.9.0, Fastify 5.6.2, fastify-metrics 12.1.0, and
prom-client 15.1.3.

## Instrument the application

Install `fastify-metrics` in your Fastify 5 application. Register the plugin
before application routes, with unmatched-route tracking enabled and
instrumentation of the metrics endpoint disabled.

```js
const app = require('fastify')();

async function start() {
  await app.register(require('fastify-metrics'), {
    routeMetrics: { registeredRoutesOnly: false },
    endpoint: { url: '/metrics', config: { disableMetrics: true } },
  });

  app.get('/orders/:id', async (request) => ({ id: request.params.id }));
  await app.listen({ port: 3000 });
}

start().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
```

The exporter defaults record registered routes, including handlers that
return 404. With `registeredRoutesOnly: false`, unmatched URLs also contribute
to requests, exact 404s, and 4xx under the shared `__unknown__` route label.

The default endpoint adds its own scrapes to the exporter's histogram.
`disableMetrics: true` excludes them at the source. StatLite also filters
the route that matches the configured metrics URL path from requests,
errors, and duration. Use a direct exporter URL whose path matches the
Fastify route. Polling and typed inspection reject redirects.

## Configure and inspect the target

```yaml
targets:
  - name: orders
    type: fastify
    url: http://127.0.0.1:3000/metrics
```

For a protected endpoint, configure the
[Basic Auth block](../configuration.md#basic-auth) for polling.

```sh
curl -i http://127.0.0.1:3000/orders/123
curl -i http://127.0.0.1:3000/no-such-route
statlite inspect --type fastify http://127.0.0.1:3000/metrics
```

Inspection reports partial compatibility until the histogram includes an
unmatched-route response. With the default exporter recipe, the warning
persists and the HTTP charts describe registered-route traffic.

## Read the dashboard

The configured recipe provides eight signals in one process.

- Requests, exact 404s, 4xx, and 5xx. An explicit HTTP 500 or an unhandled
  route exception that produces one counts as one request and one 5xx.
- Average latency from matching histogram count and duration sums. StatLite
  reads the histogram only because the summary covers the same responses.
- Process CPU in logical cores after two consecutive observations from the
  same process, V8 heap used in bytes, and restart detection from process
  start time.

Attention uses the same instrumented route population for requests and 5xx.
UP indicates successful collection. Application health is unavailable.

## Request tuple limit

StatLite accepts up to **256 distinct combinations of method, route, and
status code** in the request histogram per scrape, after excluding the
metrics route. Each observed method or status on a route can add a
combination. For example, 100 routes with three observed status codes each
for one method can yield 300 combinations. Fastify can reach the limit
sooner than Express because the Express profile uses only status labels.

If the request histogram exceeds the limit, **the entire poll fails**,
including HTTP counts and runtime samples. An isolated problem with
duration sums can instead leave valid counts available while latency is
unavailable. Review the instrumented route population if you approach the
limit.

## Deployment scope

Use a single worker with the default histogram labels. A shared worker port
can return a different worker's registry on each poll, and cluster aggregates
can lose a replaced worker's historical counts. The process timestamp is
rounded to whole seconds, so two starts within the same rounded second can
share an identity. See the
[metric reference](../metrics.md#fastify-http-and-process-metrics).
