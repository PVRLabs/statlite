# Public integration testing

StatLite's documented integrations are continuously exercised in public CI
using the shipped runnable examples and real StatLite collections.

## How it works

Each guide is the canonical integration recipe. Its companion example is a
small, maintained application that implements that recipe. The public workflow
then starts that same example, so users can inspect and run the fixture used by
the check.

Each integration case starts the application and StatLite, generates bounded
traffic, and forces one or more collections. The exact ordering varies by
adapter. In general, the workflow covers:

- Building or installing the example and waiting for its endpoint to be ready.
- Starting the current StatLite build with the example configuration and
  isolated temporary storage.
- Sending a small deterministic set of normal and error requests.
- Forcing one or more collections through StatLite's existing debug API
  instead of waiting for the polling interval.
- Checking the important basic request, error, and latency metrics, then
  confirming that results are visible through StatLite's summary and series
  APIs.

The direct `statlite-metrics/v1` cases also run `statlite inspect`, verify the
application's metrics endpoint, and confirm that polling that endpoint does
not increase application counters. The Go `net/http` and Gin cases establish
a baseline, then assert exact two-poll counter deltas and derived average
latency, including Gin's recovered pre-commit panic. The Spring Boot and
Quarkus cases check the framework-specific health or process/runtime signals
that their examples publish.

## Integrations in the public checks

The shared matrix currently exercises:

- [FastAPI](../examples/python-fastapi-demo/) through the
  [`statlite-metrics/v1` guide](integrate/python/fastapi.md).
- [Express](../examples/node-express-demo/) through the
  [`statlite-metrics/v1` guide](integrate/node/express.md).
- [Django](../examples/python-django-demo/) through the
  [`statlite-metrics/v1` guide](integrate/python/django.md).
- [Go `net/http`](../examples/go-net-http-demo/) through the
  [`statlite-metrics/v1` guide](integrate/go/net-http.md).
- [Gin](../examples/go-gin-demo/) through the
  [`statlite-metrics/v1` guide](integrate/go/gin.md).
- [Spring Boot](../examples/spring-actuator-demo/) through the Actuator
  integration.
- [Quarkus](../examples/quarkus-metrics-demo/) through the Micrometer metrics
  integration.

Inspect the implementation and current triggers in the public
[`integration certification` workflow](https://github.com/PVRLabs/statlite/actions/workflows/integration-certification.yml).
It runs for every pull request, every push to `main`, and weekly on Monday at
09:17 UTC. Matrix cases use `fail-fast: false`, so one integration failure does
not hide the results for the others.

The release workflow reuses this same workflow as a prerequisite before it
creates a release tag, so a release cannot proceed until all seven public cases
pass on the dispatched commit.

These are focused public integration checks for the documented monitoring
journey. They do not cover every framework version, production topology,
deployment environment, or application behavior, and they are not a
compatibility guarantee. Broader private and release testing remains separate
where additional coverage is needed.
