# Changelog

This file summarizes the main user-facing changes in each StatLite release.
Detailed release notes are generated on GitHub from the commit history.

## v0.3.0 — 2026-08-19

- Added `statlite inspect <application-url>` to discover supported Spring Boot
  Actuator and StatLite Metrics v1 applications and print validated target YAML.
  Inspection is bounded and read-only, works without an existing
  `statlite.yaml`, and reports actionable results for authentication,
  reachability, ambiguity, and unsupported applications.
- Improved Spring polling efficiency by deduplicating equivalent requests and
  coalescing repeated failures while preserving useful poll and restart state.
- Improved dashboard startup readiness so refresh behavior follows the active
  application run and handles transient empty or failed series more clearly.
- Added a low-resource monitoring guide and clarified resource expectations,
  metric governance, integration boundaries, historical coverage, and the
  recommended Go testing workflow.
- Expanded the Spring Actuator example and traffic recipe, including clearer
  status handling and configuration guidance.

## v0.2.3 — 2026-08-10

- Vendored Chart.js and Orbitron font assets into the dashboard so it renders
  without external CDN access, including in offline and air-gapped environments.
- Bounded historical series queries so long-range dashboard requests avoid
  scanning excessive retained history.
- Improved long-session dashboard behavior, including pausing refresh while the
  browser tab is hidden.
- Limited Spring Actuator responses to 1 MiB to keep collector memory use
  bounded.
- Updated polling defaults and examples to use 30-second intervals, with
  guidance for more resource-conscious production deployments.

## v0.2.2 — 2026-08-07

- Added the StatLite version to the dashboard header.
- Published multi-platform Docker images for `linux/amd64` and `linux/arm64`.
- Improved Python integration documentation and examples.

## v0.2.1

- Added the minimal multi-platform Docker image and Docker deployment documentation.
- Added optional Spring host metrics for CPU, memory, and disk usage.
- Improved the dashboard layout and host-resource visualization.
