# Quarkus Micrometer metrics fixture

This fixture is the pinned public source contract for StatLite's Quarkus target.
It uses Quarkus 3.39.1, Java 21 LTS, and
`quarkus-micrometer-registry-prometheus`.
The management endpoint is `http://127.0.0.1:9000/q/metrics`; the application
listens on port 8080. Run it with `./mvnw quarkus:dev` (or `mvn quarkus:dev`)
and run `./traffic.sh` from this directory. The contract validation recipe uses
Java 21 and can be run with
`JAVA_HOME=/path/to/jdk-21 ./contract/capture.sh`.

The default response is OpenMetrics (`application/openmetrics-text`). The
contract tests request Prometheus text with `Accept: text/plain`.

The fixture deliberately emits successful, 404, other 4xx, and 5xx requests.
Restart the process between scrapes to verify process identity and counter-reset
handling. Raw captures belong in `contract/`; generated logs and Maven output
are not committed.

Configure the application with the exact management metrics endpoint:

```yaml
targets:
  - name: orders
    type: quarkus
    url: http://localhost:9000/q/metrics
```

StatLite accepts the bounded Micrometer Prometheus/OpenMetrics contract and
normalizes request count and duration, 404/4xx/5xx counts, process CPU, heap
used, process start time, and optional uptime. HTTP meters are lazy, so an idle
application can still be compatible through its runtime families. Quarkus
targets do not provide application or database health fields and do not infer
host resources. Use `statlite inspect --type quarkus` with the exact endpoint;
untyped discovery intentionally does not probe `/q/metrics`.
