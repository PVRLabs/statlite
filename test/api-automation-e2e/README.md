# API automation end-to-end test

Run this manual end-to-end exercise from the public repository root:

```sh
python3 test/api-automation-e2e/run.py
```

It requires Python 3, Go, `curl`, and `jq`; no Python packages or private
fixtures are needed. The same command runs through the manually triggered
**API Automation E2E** GitHub Actions workflow. It is not part of
push or pull request checks. The runner builds the current StatLite source,
starts a synthetic `automation-demo-app`, and polls it every 10 seconds through
the normal `statlite-metrics` target path.

The fixture advances every 30 seconds of real elapsed time. Its counters and
gauges are scripted values, not generated workload or machine pressure. The
first 30 seconds provide a healthy baseline. CPU rises at 30 seconds. From
60 to 180 seconds, 5xx, latency, memory, and host fractions rise. The final
minute keeps CPU high, omits 5xx while requests grow, and reports `DOWN` near
the end. The runner waits for public minute points and invokes the scripts in
[`examples/api-automation`](../../examples/api-automation/) against `/api/v1`.
It checks their exit codes and public HTTP, CPU, runtime memory, and host
values. It does not use SQLite or dashboard APIs to decide whether StatLite
observed a phase.

Normal execution takes roughly four to five minutes. The harness has an
8-minute deadline including build and startup; the workflow allows 10
minutes. On failure, it prints the last public API responses and application
and StatLite logs. It also keeps these in a temporary directory for local
investigation. Successful runs remove their temporary files.

To explore while it runs, copy the printed `export BASE_URL=... TARGET=...`
command into another terminal, then inspect the public responses and run a
recipe:

```sh
curl -fsS --get "$BASE_URL/api/v1/status" --data-urlencode "target=$TARGET" | jq .
curl -fsS --get "$BASE_URL/api/v1/metrics" --data-urlencode "target=$TARGET" | jq '.points[-3:]'
./examples/api-automation/check-http-5xx.sh
```

The printed phase observations show when a rate becomes available, when a
high value triggers a recipe, and when missing paired 5xx data returns an
unknown result. See the [API reference](../../docs/api.md) for field
meanings and the [recipe guide](../../examples/api-automation/README.md) for
caller-owned thresholds.
