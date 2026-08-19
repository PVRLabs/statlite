# Monitoring on resource-constrained servers

StatLite is built for lightweight application monitoring on VPSs, home
servers, and other resource-constrained systems. It provides focused health,
traffic, latency, CPU, memory, and disk visibility without requiring a
Prometheus and Grafana deployment.

Low resource use is a product constraint, not an afterthought. StatLite uses a
single Go binary, stores metrics in SQLite, polls a fixed set of useful metrics,
recommends conservative polling, and defaults to bounded retention. This keeps
the monitoring system practical on the same small server as the application it
observes.

## Why a smaller monitoring stack?

For a single application on a small VPS, running Prometheus and Grafana,
deploying an OpenTelemetry collector with a separate backend, or installing a
full-featured monitoring agent such as Datadog can provide far more capability
than the deployment requires.

StatLite is not a replacement for those platforms when you need logs,
distributed tracing, arbitrary metrics, or fleet-scale observability. It
targets the smaller case where health, traffic, latency, CPU, and memory are
enough. StatLite intentionally trades breadth for a single binary, focused
application metrics, SQLite storage, and a built-in dashboard.

## Resource focus

StatLite is designed to keep its operational footprint understandable:

- one self-contained binary;
- no external metrics database or message broker;
- SQLite history with configurable retention;
- fixed Spring Boot Actuator and StatLite Metrics integrations;
- conservative polling, query, and response sizes; and
- self-monitoring for StatLite process and host-resource visibility.

Current Linux and macOS measurements show roughly 10 to 15 MiB of idle RSS for
the StatLite process. This is an observed range, not a maximum-memory guarantee
or SLA. Actual resource use depends on the number of targets, polling interval,
retention, metric availability, historical queries, and operating environment.

## Low-memory Spring Boot experiment

We tested that premise by running StatLite beside a representative Spring Boot
application on a constrained VM and measuring both services throughout
controlled observation runs.

The Spring Boot 3.5.5 application used Java 21, Spring Data JPA, Hibernate, H2,
embedded Tomcat, Actuator, scheduled polling, and outbound HTTP. The controlled
experiment compared five RAM, swap, and JVM configurations on an Ubuntu 24.04
VM with 1 vCPU and a 5 GB disk.

The 256 MB RAM configurations were unstable for the Spring Boot workload.
Spring encountered OOM restarts or failed during startup, while StatLite
remained healthy and continued monitoring. A VM configured with 512 MB RAM,
256 MB swap, and a simple low-memory JVM profile completed the 60-minute
observation cleanly:

- zero Spring Boot and StatLite restarts;
- 72 of 72 bounded HTTP checks returned 200;
- approximately 167 MiB final Spring Boot RSS;
- approximately 12 MiB final StatLite RSS;
- approximately 160 MiB swap in use; and
- approximately 140 MiB RAM available at the final checkpoint.

The result does not mean every Spring Boot application fits the same machine or
JVM profile. It demonstrates that useful StatLite monitoring can remain a small
part of the footprint beside a real application under tight memory constraints.

See the [reproducible low-memory Spring Boot
experiment](https://github.com/PVRLabs/experiments/tree/main/statlite/spring-boot-low-memory)
for the complete configuration comparison, methodology, selected sanitized
measurements, and demo application.

## Practical guidance

For a small single-host deployment:

1. Run StatLite on the same host as the application when local host-resource
   visibility is useful.
2. Start with the recommended 30-second polling interval and bounded retention.
3. Monitor StatLite itself alongside the application.
4. Keep the dashboard bound to loopback or protect access at a trusted proxy.
5. Measure the complete workload on the actual server before choosing the
   smallest viable machine.

See [Installation](install.md), [systemd deployment](systemd.md), and
[Configuration](configuration.md) for setup details.
