# Native Express smoke test

The integration workflow runs a minimal Express application with
`express-prom-bundle` and the built StatLite binary. It checks persisted
request and error counts, default scrape exclusion, and latency.

From the repository root, with Node.js and Go installed:

```sh
npm --prefix test/integration/express ci --ignore-scripts --no-audit --no-fund
go build -o /tmp/statlite ./cmd/statlite
STATLITE_BIN=/tmp/statlite node --test test/integration/express/smoke.test.cjs
EXPRESS_PROCESS_METRICS=1 STATLITE_BIN=/tmp/statlite node --test test/integration/express/smoke.test.cjs
```

The last command also checks the optional CPU, V8 heap and process-start
metrics. StatLite listens on loopback port 19090 by default. Set
`STATLITE_PORT` to use a different port.
