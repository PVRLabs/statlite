# Micronaut Micrometer demo

A self-contained public example for StatLite's explicit `micronaut` target.
Platform parent 5.2.1 pins Micronaut 5.2.11, Micronaut Micrometer 6.1.0,
and Micrometer 1.17.1. The demo compiles to Java 25 bytecode and public CI
runs it on Eclipse Temurin 25.

From this directory, with Java 25 and Maven installed:

```sh
mvn --batch-mode --no-transfer-progress package dependency:build-classpath -Dmdep.outputFile=target/classpath.txt
java -cp "target/classes:$(cat target/classpath.txt)" example.Application
```

The application binds to loopback port 18084. Management exposes ordinary
`/prometheus` metrics and `/health` aggregate UP health. No datasource is
configured, so database health is unavailable. Micronaut 5 can record the
`/prometheus` scrape in its own response, so HTTP counters may be present on
the first scrape, even before application traffic.
In another terminal, run `./traffic.sh`: exactly one request each to
`/probe/ok` (200), `/probe/missing` (404), `/probe/bad` (400), and
`/probe/error` (500). The missing route exercises the framework's normal 404.
Management requests also contribute to HTTP request counts and duration.

From the repository root, use the example configuration and typed inspection:

```sh
go run ./cmd/statlite --config examples/micronaut-metrics-demo/statlite.yaml
go run ./cmd/statlite inspect --type micronaut http://127.0.0.1:18084
```

Typed inspection checks the supported metrics contract and resolves the base
URL to `/prometheus`. Compatibility does not establish framework identity.
See the [Micronaut target reference](../../docs/targets/micronaut.md)
for normalization and optional health behavior.

To run the CI journey, stop the manually started application first, then from
the repository root:

```sh
go build -o /tmp/statlite-micronaut ./cmd/statlite
STATLITE_BIN=/tmp/statlite-micronaut ./scripts/ci/integration-micronaut.sh
```

The journey uses loopback ports 18084 and 19094, temporary SQLite storage,
an hourly polling interval with explicit debug polls, and bounded traffic.
It waits for startup collections and their possible follow-up to settle before
beginning the controlled baseline/traffic sequence.
It checks inspection, stored counter deltas, request duration, runtime signals,
application UP health, and absent database health, then stops both processes.
[Public integration testing](../../docs/integration-testing.md) describes
the shared CI and release prerequisite.
