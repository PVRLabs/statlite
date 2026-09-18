# Go net/http StatLite Metrics demo

This runnable standard-library application accompanies the canonical
[Go `net/http` integration guide](../../docs/integrate/go/net-http.md). The
guide contains the complete copyable helper and explains status ownership,
panic handling, response-writer compatibility, and process-local deployment.

## Run the demo

The tested baseline is Go 1.27.1. From this directory:

```bash
go run .
```

In another terminal, generate representative traffic and inspect the profile:

```bash
curl -s http://127.0.0.1:8080/
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/implicit
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/missing
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/client-error
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/failure
curl -s http://127.0.0.1:8080/statlite/metrics
statlite inspect 'http://127.0.0.1:8080/statlite/metrics'
```

From the public repository root, start StatLite with the demo configuration:

```bash
go run ./cmd/statlite --config examples/go-net-http-demo/statlite.yaml
```

Open <http://127.0.0.1:9090>. The demo polls every 10 seconds for responsive
local feedback. Use a 30-second or longer interval in production. Its SQLite
file is created under `examples/go-net-http-demo/`.

## Run the tests

From this directory:

```bash
go test ./...
go test -race ./...
```

The tests include real HTTP server checks for streaming flush and connection
hijacking. They also prove that an escaping panic is not recorded, while
application recovery inside the StatLite middleware can write and record 500.
