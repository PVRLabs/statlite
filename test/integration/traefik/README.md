# Traefik service certification

Use Python 3.15, a built StatLite binary, and the official
[Traefik 3.8.0 binary](https://github.com/traefik/traefik/releases/tag/v3.8.0)
for your platform. The tested darwin/amd64 archive has SHA-256
`2d29524cf056c9f280a42598c4295ed689d4973f26b9b6895e6dc60617ba13c8`.

```sh
go build -o /tmp/statlite-traefik ./cmd/statlite
python3 test/integration/traefik/run.py /tmp/statlite-traefik /path/to/traefik /tmp/traefik-run
```

Use a fresh output directory. The fixture binds loopback ports 18980–18985.
It runs two HTTP backends, a file-provider proxy, and a dedicated metrics
listener. The script compares real HTTP responses, persisted polls, exporter
duration sums and the dashboard API. It records latency coverage under
concurrent polling and traffic. For Attention, it copies actual poll pairs
and spaces their timestamps 290 seconds apart in disposable databases.
A second StatLite process with polling disabled evaluates the controlled
windows. The original live history is preserved.

The output includes a JSON report, configs, logs, SQLite data and typed
inspection output. Pass `--hold 90` to keep the live dashboard available for
browser inspection after the scripted checks.
