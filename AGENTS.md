
# AGENTS.md

Guidance for AI coding agents working on StatLite.

If `AGENTS.local.md` exists, read it after this file.

## Project Summary

StatLite is a small, self-hosted, SQLite-backed metrics dashboard for small
applications and VPS deployments. It supports a closed set of framework and
application integrations, with Spring Boot Actuator as the reference
integration, and provides a lightweight alternative to Prometheus and Grafana
when a general-purpose observability stack is unnecessary. See `README.md` and
`docs/` for user-facing documentation.

## Implementation Constraints

* Prefer maintainable, explicit Go code and clear package boundaries over feature breadth, framework-heavy abstractions, or speculative abstractions.
* Keep the binary and runtime footprint small. Treat memory, CPU, disk growth, network activity, goroutine count, and response cardinality as product constraints. Use conservative production defaults; put faster polling or higher cardinality behind explicit configuration or clearly labeled demos.
* Keep source- and framework-specific details inside collector adapters and use normalized internal concepts elsewhere.
* Make the smallest useful change and do not expand product scope without explicit approval.
* Add tests where logic can regress, make errors descriptive, and keep docs in sync when behavior changes.
* Use SQLite through Go `database/sql`. Prefer `modernc.org/sqlite` unless there is a concrete reason to switch.
* Commit only when explicitly requested.
* When the active implementation plan specifies an issue number, include it (for example, `#14`) in every related commit message.

## Product Boundaries

Follow `docs/product.md` for product scope; expand scope only when explicitly
requested.

## Data Model Guardrails

Preserve the poll and storage model documented in `docs/product.md`:

* persist each poll as one logical collection result with its raw normalized samples;
* keep raw samples authoritative and derive counter deltas at query time without exposing negative deltas; and
* preserve useful partial collections and record collector warnings or errors instead of hiding missing or invalid data.

## Verification

Use the `lite-tools` skill for routine npm/Node and Go test workflows.

For dashboard changes, run
`npm-lite node --test internal/dashboard/static/dashboard.test.js`; Go tests do
not cover the dashboard.

Break large changes into independently testable slices; clarify scope when
ambiguous.

## Related Repositories and Release Tooling

Use the `repo-map` skill to discover related repositories and their local paths.
Project-specific workflow instructions remain authoritative. When a Homebrew
formula update is in scope, use `repo-map get homebrew-tap` to locate the
shared PVRLabs tap.

## Capitalization Convention

Use `StatLite` for user-facing product prose. Use `statlite` for internal identifiers, package/module paths, binary names, config filenames, URLs, JSON fields, target type values, and command examples.

## Writing Style

Do not add em dashes to new or edited documentation or other user-facing prose.
