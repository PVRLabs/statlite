
# AGENTS.md

Guidance for AI coding agents working on StatLite.

## Project Summary

StatLite is a tiny self-hosted metrics dashboard for small servers.

The MVP is Spring Boot Actuator-first, SQLite-backed, and intentionally small. It is not a Prometheus/Grafana replacement.

User-facing docs: `README.md`, `docs/configuration.md`. Install and release docs may also live under `docs/` when present.

## Working Principles

* Prefer maintainability and simplicity over feature breadth.
* Keep the binary and runtime footprint small.
* Prefer boring, explicit Go code over framework-heavy abstractions.
* Keep Spring Boot / Actuator details inside collector-facing code where practical.
* Keep storage/query/dashboard code based on normalized internal data, not raw Actuator response shapes.
* Do not add broad abstractions before the MVP needs them.
* Do not expand product scope without explicit approval.

## MVP Guardrails

Unless explicitly requested, do not implement:

* Prometheus scraping
* arbitrary metric definitions
* alert manager
* logs or traces
* dashboard auth
* plugin systems
* derived delta tables
* rollup tables
* ORM-based storage
* Kubernetes-first deployment

## Data Model Guardrails

For the MVP:

* treat each poll cycle as one logical snapshot
* store raw poll snapshots and raw metric samples
* compute counter deltas at query/API time
* never display negative counter deltas
* handle missing optional metrics gracefully
* record collector warnings/errors instead of hiding them

Use SQLite through Go `database/sql`. Prefer `modernc.org/sqlite` unless there is a concrete reason to switch.

## Repository Shape

* `cmd/statlite/main.go` — CLI entrypoint
* `internal/config` — YAML config loading and validation
* `internal/collector` — metric collection and normalization
* `internal/storage` — SQLite persistence and query logic
* `internal/server` — local HTTP server
* `internal/dashboard` — dashboard rendering / chart data shaping
* `internal/version` — central build version string

## Change Style

For implementation work:

* make the smallest useful change
* keep package boundaries clear
* add tests where logic can regress
* avoid speculative abstractions
* make errors descriptive
* keep docs in sync when behavior changes

For large or ambiguous changes, propose the smallest independently testable slice first.

## Capitalization Convention

Use `StatLite` for user-facing product prose. Use `statlite` for internal identifiers, package/module paths, binary names, config filenames, URLs, JSON fields, target type values, and command examples.
