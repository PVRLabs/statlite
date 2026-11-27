# Express application monitoring

Use `type: express` to monitor one Express application process through
`express-prom-bundle`. The verified recipes use Node.js 25.9.0, Express 5.1.0,
express-prom-bundle 8.0.0, and prom-client 15.1.3.

## Instrument the application

Install `express-prom-bundle` and its peer dependency `prom-client` in your
application. Register the bundle before the routes and middleware whose
responses you want to measure. The bundle serves `/metrics` on the application
listener and excludes its own scrapes from the default histogram.

Use the default recipe for HTTP metrics.

```js
const express = require('express');
const bundle = require('express-prom-bundle');

const app = express();
app.use(bundle({}));

app.get('/orders', (_request, response) => response.send('ok'));
app.listen(3000);
```

For HTTP and runtime metrics, use this middleware registration in the same
place in your application.

```js
app.use(bundle({promClient: {collectDefaultMetrics: {}}}));
```

## Configure and inspect the target

```yaml
targets:
  - name: orders
    type: express
    url: http://127.0.0.1:3000/metrics
```

Set the metrics URL to an address reachable from StatLite. For a protected
endpoint, configure polling credentials in the
[Basic Auth block](../configuration.md#basic-auth).

```sh
statlite inspect --type express http://127.0.0.1:3000/metrics
```

Typed inspection reports partial compatibility before the first measured
response initializes the HTTP histogram. Send a request to an application
route and inspect again to see the HTTP capabilities. The process-metrics
recipe also reports CPU, V8 heap, and process-start capabilities.

## Read the dashboard

| Signal | Default recipe | With process metrics |
| --- | --- | --- |
| Requests, exact 404, 4xx, and 5xx | Available | Available |
| Average response time | Available with matching histogram sums and counts | Available with matching histogram sums and counts |
| CPU in logical cores | Unavailable | Available after two consecutive observations from the same process |
| V8 heap used | Unavailable | Available |
| Process restart detection | Unavailable | Available when the process start timestamp changes |
| Verified coverage | 5 of 8 signals | 8 of 8 signals |

Unmatched URLs count toward exact 404 and 4xx. Explicit 500s and unhandled
exceptions that return HTTP 500 each contribute one request and one 5xx.
Average latency measures completed responses in the same histogram
population as the HTTP counts. UP indicates successful metrics collection.
Application health is unavailable through this exporter profile.

Both recipes establish a fresh HTTP baseline after a failed scrape or an
observed counter reset. Without process identity, a newly observed positive
status counter also starts a fresh baseline. The process-metrics recipe uses
a process start timestamp rounded to the nearest second, so two starts
within the same rounded second can share an identity. See the
[Express metric reference](../metrics.md#express-http-and-process-metrics)
for continuity details.

Use a single application process with the default histogram and status
labels. Cluster aggregates and ordinary scrapes through a shared worker port
are outside this profile. Worker replacement can remove a worker's historical
counts, and a shared port can return a different worker's registry on each poll.
