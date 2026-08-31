# StatLite examples

Starter configurations and runnable demo apps for monitoring applications with
StatLite. See [Configuration](../docs/configuration.md) for the full settings
reference and [StatLite Metrics v1](../docs/statlite-metrics-v1.md) for the
fixed JSON endpoint profile.

## Config files

| File | Purpose |
|------|---------|
| `actuator.yaml` | Single Spring Boot Actuator target with Basic Auth placeholders |
| `statlite.yaml` | Monitor another StatLite instance via `statlite-metrics` |
| `multi-target.yaml` | Mixed targets: Actuator, StatLite Metrics, and self-monitoring |

Run a config from the repository root, for example:

```bash
go run ./cmd/statlite --config examples/actuator.yaml
```

## Demo apps

| Directory | What it shows |
|-----------|---------------|
| [spring-actuator-demo](spring-actuator-demo/) | Runnable Spring Boot app with Actuator and Micrometer metrics, traffic generator, and dashboard preview |
| [quarkus-metrics-demo](quarkus-metrics-demo/) | Pinned Quarkus 3.39.1 Micrometer metrics fixture, contract captures, and traffic recipe |
| [python-fastapi-demo](python-fastapi-demo/) | FastAPI app exposing `statlite-metrics/v1` with a copyable middleware helper and tests |

Each demo directory has its own README with run and verification steps.
