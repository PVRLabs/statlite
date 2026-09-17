# Express StatLite Metrics demo

This tiny application accompanies the canonical
[Express integration guide](../../docs/integrate/node/express.md). The guide
contains the complete copyable integration and explains its fields,
single-process scope, and deployment caveats.

## Run the demo

Use Node.js 24.21.0 LTS and install the pinned Express dependency:

```bash
npm install
npm start
```

In another terminal, exercise normal, missing, error, and metrics responses:

```bash
curl -s http://127.0.0.1:3000/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3000/missing
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:3000/failure
curl -s http://127.0.0.1:3000/statlite/metrics
statlite inspect 'http://127.0.0.1:3000/statlite/metrics'
```

From the repository root, start StatLite with the demo configuration:

```bash
go run ./cmd/statlite --config examples/node-express-demo/statlite.yaml
```

Open <http://127.0.0.1:9091>. The demo polls every 10 seconds for responsive
local feedback. Use a 30-second or longer interval in production.

## Run the tests

From this directory:

```bash
npm test
```

The test starts the actual Express application and checks normal, 404, and 500
responses. It also verifies that repeated `/statlite/metrics` polling does not
increment application counters.
