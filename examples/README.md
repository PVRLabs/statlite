# StatLite examples

Starter configurations and runnable demo apps for monitoring applications with
StatLite. See [Configuration](../docs/configuration.md) for the full settings
reference, [StatLite Metrics v1](../docs/statlite-metrics-v1.md) for the fixed
JSON endpoint profile, and the [integration guides](../docs/integrate/) for
FastAPI, Express, Django, Go `net/http`, and Gin application setup.

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
| [python-fastapi-demo](python-fastapi-demo/) | Runnable companion to the canonical [FastAPI guide](../docs/integrate/python/fastapi.md), with middleware and framework-level tests |
| [node-express-demo](node-express-demo/) | Runnable companion to the canonical [Express guide](../docs/integrate/node/express.md), with middleware and framework-level tests |
| [python-django-demo](python-django-demo/) | Runnable companion to the canonical [Django guide](../docs/integrate/python/django.md), with middleware and framework-level tests |
| [go-net-http-demo](go-net-http-demo/) | Runnable companion to the canonical [Go `net/http` guide](../docs/integrate/go/net-http.md), with standard-library middleware and tests |
| [go-gin-demo](go-gin-demo/) | Runnable companion to the canonical [Gin guide](../docs/integrate/go/gin.md), with Gin middleware, recovery ordering, and tests |

Each demo directory has its own README with run and verification steps.
