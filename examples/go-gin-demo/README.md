# Go Gin StatLite Metrics demo

This runnable Gin application accompanies the canonical
[Go Gin integration guide](../../docs/integrate/go/gin.md). The guide contains
the complete copyable helper and explains recovery ordering, status ownership,
streaming behavior, and process-local deployment.

## Run the demo

The tested baseline is Go 1.27.1 with Gin 1.12.0. From this directory:

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
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/panic
curl -s http://127.0.0.1:8080/statlite/metrics
statlite inspect 'http://127.0.0.1:8080/statlite/metrics'
```

From the public repository root, start StatLite with the demo configuration:

```bash
go run ./cmd/statlite --config examples/go-gin-demo/statlite.yaml
```

Open <http://127.0.0.1:9090>. The demo polls every 10 seconds for responsive
local feedback. Use a 30-second or longer interval in production. Its SQLite
file is created as `statlite-go-gin-demo.sqlite` in the repository root.

## Run the tests

From this directory:

```bash
go test ./...
go test -race ./...
```

The tests prove the required `StatLite`, `gin.Recovery()` ordering, normal and
error counters, concurrent safety, metrics-path exclusion, and Gin writer
streaming. Requests advertising conventional protocol upgrades and WebSocket
handshakes are excluded before routing, including rejected upgrade attempts,
because they might become hijacked connections without trustworthy HTTP status
or duration semantics. Arbitrary raw hijacks without those headers are outside
the certified metrics path. The tests also demonstrate that a panic after a
response is committed retains the committed status rather than being
synthesized as a 500.
