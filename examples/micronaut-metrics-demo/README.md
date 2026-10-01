# Micronaut Micrometer demo

A self-contained public example for StatLite's explicit `micronaut` target.
Platform parent 4.9.2 pins Micronaut 4.9.9, Micronaut Micrometer 5.12.0,
and Micrometer 1.15.0. Sources compile to Java 17 bytecode. Public CI builds
and runs with Eclipse Temurin Java 21 LTS; private certification uses its
separately pinned Java 25 runtime.

From this directory, with Java 21 and Maven installed:

```sh
mvn --batch-mode --no-transfer-progress package dependency:build-classpath -Dmdep.outputFile=target/classpath.txt
java -cp "target/classes:$(cat target/classpath.txt)" example.Application
```

The application binds to loopback port 18084. Management exposes ordinary
`/prometheus` metrics and `/health` aggregate UP health. No datasource is
configured, so database health is unavailable. HTTP timers appear after traffic.
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
See [configuration](../../docs/configuration.md#micronaut-micrometer-metrics)
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
