
# AGENTS.md

Guidance for AI coding agents working on StatLite.

If `AGENTS.local.md` exists, read it after this file.

## Project Summary

StatLite is a small, self-hosted, SQLite-backed metrics dashboard focused on Spring Boot Actuator and intentionally not a Prometheus/Grafana replacement. See `README.md` and `docs/` for user-facing documentation.

## Implementation Constraints

* Prefer maintainable, explicit Go code and clear package boundaries over feature breadth, framework-heavy abstractions, or speculative abstractions.
* Keep the binary and runtime footprint small. Treat memory, CPU, disk growth, network activity, goroutine count, and response cardinality as product constraints. Use conservative production defaults; put faster polling or higher cardinality behind explicit configuration or clearly labeled demos.
* Keep Actuator details in collector-facing code and use normalized internal data elsewhere.
* Make the smallest useful change and do not expand product scope without explicit approval.
* Add tests where logic can regress, make errors descriptive, and keep docs in sync when behavior changes.
* Do not create commits unless the user explicitly approves committing after reviewing the changes.
* When the active implementation plan specifies an issue number, include it (for example, `#14`) in every related commit message.

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

## Verification

When changing `internal/dashboard/static/index.html`, `dashboard.js`, or
`dashboard.test.js`, run the dashboard unit tests locally:

```bash
npm-lite node --test internal/dashboard/static/dashboard.test.js
```

`go-lite test ./...` intentionally does not run Node tests; it is the preferred
Go-only verification command for unrelated changes. Use `go-lite --full test
./...` when complete live output is needed.

For large or ambiguous changes, propose the smallest independently testable slice first.

## Related Repositories and Release Tooling

Use `repo-map` to discover related repositories and their local paths and broad
roles. Use `repo-map commands` to discover shared agent-oriented tools available
on `PATH`, including low-noise build and test wrappers and browser helpers;
invoke them by name. Project-specific workflow instructions remain
authoritative. When a Homebrew formula update is in scope, use
`repo-map get homebrew-tap` to locate the shared PVRLabs tap.

## Capitalization Convention

Use `StatLite` for user-facing product prose. Use `statlite` for internal identifiers, package/module paths, binary names, config filenames, URLs, JSON fields, target type values, and command examples.

## Writing Style

Do not add em dashes to new or edited documentation or other user-facing prose.
