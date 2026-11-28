# Native Fastify smoke test

Run from the repository root with Node.js 25.9.0.

```sh
go build -trimpath -o /tmp/statlite-fastify ./cmd/statlite
npm --prefix test/integration/fastify ci --ignore-scripts --no-audit --no-fund
STATLITE_BIN=/tmp/statlite-fastify node --test test/integration/fastify/smoke.test.cjs
```

The fixture starts one Fastify process with the recommended exporter options.
It checks that scrapes add no requests and that the StatLite series records
an unmatched 404, an unhandled 500, a success, and available latency.
Set `STATLITE_PORT` if the default loopback port 19090 is in use.
