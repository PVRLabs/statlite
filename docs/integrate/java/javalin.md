# Integrate Javalin with StatLite Metrics

A Javalin application can expose `GET /statlite/metrics` as
`statlite-metrics/v1`. StatLite polls that URL with `type: statlite-metrics`.

Use the [runnable example](https://github.com/PVRLabs/experiments/tree/main/statlite/javalin-micrometer)
to try this recipe. Its `StatLiteMetrics.java` is the same helper shown below.
The example uses Javalin and `javalin-micrometer` 7.2.3, which brings Micrometer
1.17.0, and Jackson Databind 2.22.1 for `ctx.json`. These are the example's
pinned versions, not a compatibility guarantee for other setups.

Javalin owns its Micrometer instrumentation; your application owns the adapter
and endpoint. StatLite consumes the generic v1 contract. There is no native
`type: javalin` target, and this recipe is outside integration CI.

## Dependencies

Use Java 17 or higher, as required by [Javalin 7](https://javalin.io/documentation).
No StatLite SDK. Add the Micrometer plugin at the same version as Javalin.
That plugin brings the Micrometer API this helper uses. Do not add a second
Micrometer pin for StatLite. Include Jackson Databind for the default JSON
mapper used by `ctx.json`:

```xml
<dependency>
    <groupId>io.javalin</groupId>
    <artifactId>javalin</artifactId>
    <version>7.2.3</version>
</dependency>
<dependency>
    <groupId>io.javalin</groupId>
    <artifactId>javalin-micrometer</artifactId>
    <version>7.2.3</version>
</dependency>
<dependency>
    <groupId>com.fasterxml.jackson.core</groupId>
    <artifactId>jackson-databind</artifactId>
    <version>2.22.1</version>
</dependency>
```

[Javalin uses Jackson as its default JSON mapper](https://javalin.io/documentation#configuring-the-json-mapper).
With Jackson Databind on the classpath, no `config.jsonMapper` call is needed.
Without it or another configured JSON mapper, `ctx.json` fails and
`GET /statlite/metrics` returns HTTP 500.

If your application already configures a JSON mapper, keep it and its
required dependencies; you do not need to add Jackson for this endpoint.
The mapper setting applies to every route, so replacing it can change
serialization outside `/statlite/metrics`. An existing SLF4J provider is
enough. This integration does not add one.

Use a cumulative `SimpleMeterRegistry`, as in the example. No Prometheus
dependency or server is required. Bind `JvmMemoryMetrics` and `ProcessorMetrics`
once. If your application already has a cumulative registry, reuse it.

## Copyable helper

Save this as `StatLiteMetrics.java` and change the package to match the
application.

```java
// Source and updates: https://github.com/PVRLabs/statlite/blob/main/docs/integrate/java/javalin.md

package example;

import io.micrometer.core.instrument.Gauge;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Timer;
import java.lang.management.ManagementFactory;
import java.time.Instant;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.TimeUnit;

public final class StatLiteMetrics {
    public static final String PATH = "/statlite/metrics";

    // Micrometer's uri tag is the matched route template.
    // PATH covers /statlite/metrics. Add other instrumentation routes if used.
    private static final Set<String> EXCLUDED_URIS = Set.of(PATH);

    private StatLiteMetrics() {}

    public static Map<String, Object> snapshot(MeterRegistry registry, String status) {
        if (status == null || status.isBlank()) {
            throw new IllegalArgumentException("StatLite application status must not be empty");
        }

        double requests = 0;
        double notFound = 0;
        double clientErrors = 0;
        double serverErrors = 0;
        double seconds = 0;
        for (Timer timer : registry.find("http.server.requests").timers()) {
            String uri = timer.getId().getTag("uri");
            if (uri != null && EXCLUDED_URIS.contains(uri)) {
                continue;
            }
            long count = timer.count();
            requests += count;
            seconds += timer.totalTime(TimeUnit.SECONDS);
            String code = timer.getId().getTag("status");
            if ("404".equals(code)) {
                notFound += count;
            }
            if (code != null && code.startsWith("4")) {
                clientErrors += count;
            }
            if (code != null && code.startsWith("5")) {
                serverErrors += count;
            }
        }

        Map<String, Object> metrics = new LinkedHashMap<>();
        metrics.put("requests_total", requests);
        metrics.put("responses_404_total", notFound);
        metrics.put("responses_4xx_total", clientErrors);
        metrics.put("responses_5xx_total", serverErrors);
        metrics.put("request_duration_seconds_total", seconds);

        double heap = 0;
        boolean heapSeen = false;
        for (Gauge gauge : registry.find("jvm.memory.used").tag("area", "heap").gauges()) {
            double value = gauge.value();
            if (Double.isFinite(value) && value >= 0) {
                heap += value;
                heapSeen = true;
            }
        }
        if (heapSeen) {
            metrics.put("runtime_heap_used_bytes", heap);
        }

        Gauge cpu = registry.find("process.cpu.usage").gauge();
        if (cpu != null) {
            double cpuFraction = cpu.value();
            if (Double.isFinite(cpuFraction) && cpuFraction >= 0) {
                metrics.put(
                    "process_cpu_usage",
                    cpuFraction * Runtime.getRuntime().availableProcessors());
            }
        }

        var runtime = ManagementFactory.getRuntimeMXBean();
        double uptimeSeconds = runtime.getUptime() / 1000.0;
        if (Double.isFinite(uptimeSeconds) && uptimeSeconds >= 0) {
            metrics.put("uptime_seconds", uptimeSeconds);
        }

        Map<String, Object> body = new LinkedHashMap<>();
        body.put("schema", "statlite-metrics/v1");
        body.put("integration", "javalin");
        body.put("status", status);
        body.put("started_at", Instant.ofEpochMilli(runtime.getStartTime()).toString());
        body.put("metrics", metrics);
        return body;
    }
}
```

`EXCLUDED_URIS` must match the route templates Micrometer records. `PATH` is
the metrics route, so changing `StatLiteMetrics.PATH` updates that exclusion.
If the application also exposes `/prometheus`, add it to the set, for example
`Set.of(PATH, "/prometheus")`. Add or update entries when instrumentation
routes change so polling them is not counted as application traffic.

## Register

`health` starts at `UP` and stays there until the application's readiness check
changes it. `setReady` is that point. A metrics response of HTTP 200 does not
mean the application is ready.

```java
package example;

import io.javalin.Javalin;
import io.javalin.micrometer.MicrometerPlugin;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.binder.jvm.JvmMemoryMetrics;
import io.micrometer.core.instrument.binder.system.ProcessorMetrics;
import io.micrometer.core.instrument.simple.SimpleMeterRegistry;
import java.util.concurrent.atomic.AtomicReference;

public final class Application {
    static final AtomicReference<String> health = new AtomicReference<>("UP");

    public static void setReady(boolean ready) {
        health.set(ready ? "UP" : "DOWN");
    }

    public static void main(String[] args) {
        MeterRegistry registry = new SimpleMeterRegistry();
        new JvmMemoryMetrics().bindTo(registry);
        new ProcessorMetrics().bindTo(registry);

        Javalin app = Javalin.create(config -> {
            config.registerPlugin(new MicrometerPlugin(plugin -> plugin.registry = registry));
            config.routes.get(StatLiteMetrics.PATH, ctx -> {
                ctx.header("Cache-Control", "no-store");
                ctx.json(StatLiteMetrics.snapshot(registry, health.get()));
            });
        }).start(8080);

        Runtime.getRuntime().addShutdownHook(new Thread(() -> {
            app.stop();
            registry.close();
        }));
    }
}
```

Register the plugin and the route before `start`. Leave the application's
listen address and JSON mapper unchanged. Reuse an existing cumulative
registry instead of binding those meters twice. Restrict access to
`/statlite/metrics` with network and firewall rules, or source restrictions
at a reverse proxy. The StatLite Metrics target does not send application
authentication credentials, so do not require Basic or Bearer authentication
from StatLite on this route.

## Configure StatLite

```yaml
server:
  listen: "127.0.0.1:9090"

storage:
  sqlite_path: "./statlite.sqlite"

polling:
  interval: "30s"

targets:
  - name: "my-javalin-app"
    type: "statlite-metrics"
    url: "http://127.0.0.1:8080/statlite/metrics"
```

## Verify collection

The snippets above use example ports `8080` for Javalin and `9090` for
StatLite. For an existing application, keep its host and port and update the
target URL and verification commands to match. The runnable experiment uses
`18087` for Javalin and `19087` for StatLite; use those ports when following
the experiment, including <http://127.0.0.1:19087> for its dashboard.

Start the application and check the endpoint:

```sh
curl -i http://127.0.0.1:8080/statlite/metrics
statlite inspect 'http://127.0.0.1:8080/statlite/metrics'
statlite --config statlite.yaml
```

The response should be JSON with `schema: statlite-metrics/v1`, a nonempty
application `status`, `started_at`, and cumulative HTTP counters under `metrics`.
Before traffic, the HTTP counters are zero. Generate requests to your normal
routes and confirm the counters rise; repeated metrics polls must not raise them.
Open <http://127.0.0.1:9090> to view the dashboard. The runnable example includes
`probe.py` to generate and check successful, slow, 404, and 500 traffic. Run
that probe with the example's `statlite.yaml`, which uses a `2s` polling
interval. Its 15-second timeout is too short for the `30s` interval shown
above: StatLite polls once at startup, then waits for the configured interval.

`requests_total` and `request_duration_seconds_total` sum the
`http.server.requests` timers after route exclusions. HTTP error counters use
the timers' `status` tags; 404 is also included in 4xx. Heap and process CPU
are optional and omitted when their gauges are unavailable or invalid.
`started_at` identifies the JVM run so counter deltas do not cross restarts.

## Caveats

- Use a cumulative registry. A step registry reports interval values, not
  process-lifetime counters.
- `status` is the application's own health value. It stays `UP` until
  readiness code calls `setReady` or another `health.set`.
- The registry belongs to one process. Do not poll a load-balanced URL across
  independent processes.
- `runtime_heap_used_bytes` is JVM heap used. It is not process RSS, container
  memory, or a maximum heap size.
- Javalin 7.2.3 records request time in whole milliseconds, so a
  sub-millisecond request can add zero duration.

- Counts and durations are sampled separately and can briefly differ during
  concurrent traffic. Process CPU is a best-effort JVM measurement in cores.
- Connect readiness to an authoritative inexpensive or cached application
  signal. Do not infer dependency health from requests or run network/database
  checks on every metrics poll.
