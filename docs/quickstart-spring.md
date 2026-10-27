# Monitor a Spring Boot Application in 5 Minutes

Monitor your existing Spring Boot application: enable Actuator and let StatLite
collect application metrics into a local SQLite database with a built-in
dashboard. No Prometheus, Grafana, Docker, or monitoring agent is required.
Allow extra time for initial dependency downloads.

Verified on macOS with Spring Boot 4.1.2, Java 25, Maven 3.9.9, and the
published StatLite v0.7.0 release. The commands also target Linux; that platform
was not exercised for this guide.

You need macOS or Linux, a compatible JDK (Java 25 for the optional example),
Maven or your application's existing build tool, and `curl`. Creating the optional sample
also needs `unzip` and a free port **8080**. Keep port **9090** free for the
dashboard.

## 1. Prepare your application (or create a sample)

**Existing application:** start with your Spring Boot application and an HTTP
endpoint. This guide uses 4.1.2; see the [integration support matrix](integrations.md)
for other certified versions. Add Actuator if it is not already included, using
your project's build tool. For Maven, add this dependency, with its version
managed by Spring Boot:

```xml
<dependency>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-actuator</artifactId>
</dependency>
```

Continue at step 2. Retain your existing build and startup commands, listener
settings, compatible JDK, and application port. StatLite does not require changing
your application's Java version. Use your actual application URL and an existing
HTTP endpoint wherever the commands below use `http://127.0.0.1:8080` and `/hello`.

**No application yet:** generate a Maven project with Spring Web and Actuator
using [Spring Initializr](https://start.spring.io/). This example pins Spring
Boot 4.1.2 and targets Java 25:

```sh
mkdir spring-monitoring
cd spring-monitoring
curl -fsSL https://start.spring.io/starter.zip \
  -d type=maven-project -d language=java -d bootVersion=4.1.2 \
  -d javaVersion=25 -d groupId=com.example -d artifactId=hello \
  -d name=hello -d packageName=com.example.hello \
  -d dependencies=web,actuator -o hello.zip
unzip -q hello.zip -d hello
cd hello
cat > src/main/java/com/example/hello/HelloController.java <<'EOF'
package com.example.hello;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class HelloController {
    @GetMapping("/hello")
    public String hello() {
        return "Hello from Spring Boot!\n";
    }
}
EOF
```

## 2. Enable metrics and start the application

In your application's configuration, expose `health` and `metrics`. For
`src/main/resources/application.properties`, the setting is:

```properties
management.endpoints.web.exposure.include=health,metrics
```

Merge these endpoint names into any existing exposure list. **For the sample
application only**, also add `server.address=127.0.0.1` to keep its unauthenticated
endpoints local. Existing applications should retain their listener settings
and access controls.

Actuator exposes only health over HTTP by default; **`metrics` must be included**.
This recipe reads Actuator JSON and needs no `micrometer-registry-prometheus`.
Keep this unauthenticated local setup on loopback. For an existing application
with Spring Security, retain its protection and see
[Basic Auth configuration](configuration.md#basic-auth); `inspect` cannot
inspect authenticated endpoints. For authenticated endpoints or a custom
Actuator base path, use [manual Spring target configuration](targets/spring.md)
with your actual management base URL instead of discovery in step 4.

Rebuild and start your existing application with its normal commands so the
dependency and configuration changes take effect. **For the generated sample**, use:

```sh
mvn package
java -jar target/hello-0.0.1-SNAPSHOT.jar
```

Wait for the application's startup message. In a **second terminal**, change
to the same project directory and verify. These commands use the sample's
port, endpoint, and Actuator base path; substitute your application's actual
addresses, including a separate management port or custom path if configured:

```sh
curl -fsS http://127.0.0.1:8080/hello
curl -fsS http://127.0.0.1:8080/actuator/health
curl -fsS http://127.0.0.1:8080/actuator/metrics/http.server.requests
curl -fsS 'http://127.0.0.1:8080/actuator/metrics/jvm.memory.used?tag=area:heap'
```

Expect your endpoint's response (the greeting for the sample), health `UP`, and
JSON metrics with `measurements`. The first application request creates the
HTTP meter; before any traffic, `http.server.requests` can return 404.
If `/actuator/metrics` returns 404, check endpoint exposure and restart the
application. If only `http.server.requests` returns 404, generate some HTTP
traffic and verify that HTTP request observations are enabled.

## 3. Install StatLite

Install the latest native release, then make its default install directory
available in this terminal:

```sh
curl -fsSL https://raw.githubusercontent.com/PVRLabs/statlite/main/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"
statlite --version
```

The installer verifies the release archive's checksum. If it reports a different
install directory, add that directory to `PATH` instead. See
[Installation](install.md) for Homebrew and alternative methods.

## 4. Discover the application

From the project directory in the second terminal, inspect your application's
base URL. The sample uses port 8080; replace this URL with your actual host and
port. If Actuator runs on a separate management port, use that port instead.
Discovery expects Actuator at `/actuator`; custom paths need the manual
configuration linked above.

```sh
statlite inspect 'http://127.0.0.1:8080' --create-config ./statlite.yaml
```

StatLite detects Spring Boot Actuator and writes a complete configuration for
the detected management endpoint (`http://127.0.0.1:8080/actuator` for the sample),
with a 30-second polling interval and a local SQLite database. The command
refuses to overwrite an existing `statlite.yaml`.
For an existing monitoring configuration, see
[target discovery](configuration.md#discover-a-target-with-inspect).

## 5. Open the dashboard and generate traffic

Keep the application running and start StatLite in the second terminal:

```sh
statlite --config ./statlite.yaml
```

Open <http://127.0.0.1:9090>. If port 9090 is already in use, change
`server.listen` in `statlite.yaml` to `127.0.0.1:19090` before starting and
open <http://127.0.0.1:19090> instead. Select the Spring target
(`spring-127-0-0-1-8080` for the sample) if needed. StatLite polls immediately;
the first
counter sample establishes a baseline, with an initial follow-up after about
three seconds. Traffic charts need changes between polls.

In a **third terminal**, generate traffic for about 40 seconds. For your own
application, replace the sample's URL and `/hello` with a safe existing endpoint,
and use a path that actually returns 404 instead of `/no-such-route`:

```sh
for i in $(seq 1 40); do
  curl -fsS http://127.0.0.1:8080/hello > /dev/null
  curl -sS -o /dev/null http://127.0.0.1:8080/no-such-route
  sleep 1
done
```

Wait another **30 seconds** for the next poll and dashboard refresh. Expect
request activity, average response time, and 404/4xx activity from the missing
route, plus JVM heap used, process CPU (when available), uptime, and application
health. 5xx activity needs actual server errors. HTTP measurements aggregate
the application's requests, including Actuator calls. Idle CPU can be near
zero; missing optional metrics may remain blank. JVM heap used is runtime
memory, not total process memory or maximum heap size.

![Representative Spring Boot application dashboard](images/dashboard.webp)

This existing screenshot illustrates the dashboard with a richer application
and workload; your endpoint names and values will differ. Stop StatLite and
the application with Ctrl-C when finished. Keep `statlite.sqlite` beside the
generated configuration to preserve history across runs.

## From your laptop to a small VPS

The same setup works on a Linux VPS: run your Spring Boot JAR and StatLite as
separate native processes. Update the target address if the management port,
host, or path changes. Preserve the SQLite database on persistent storage;
relative database paths resolve beside the configuration file.

Keep Actuator private or authenticated. StatLite has no built-in dashboard
authentication: retain its loopback listener and use secure access such as an
SSH tunnel or VPN. See [installation](install.md),
[storage configuration](configuration.md#storage), and the security guidance
for [dashboard access](configuration.md#server) and
[Actuator authentication](configuration.md#basic-auth).

## More options

- [Existing Spring Boot demo](../examples/spring-actuator-demo/) with a richer traffic generator
- [Docker deployment](docker.md), an optional alternative to the native process
- [General configuration reference](configuration.md) and [Spring target details](targets/spring.md)
- [Other supported integrations](integrations.md)
