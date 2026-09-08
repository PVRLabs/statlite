# Polling, processing, and dashboard review

Status: Brainstorming
Created: 2026-09-07
Issue: #44 (https://github.com/paseo-verde-research/statlite/issues/44)

## Summary

Directional implementation report from a bounded source review. The first three correctness findings and first three performance opportunities carry forward the initial review; the remaining observations extend it with dead-code, duplication, and consistency checks. No application changes, regression reproductions, benchmarks, or runtime profiling were performed for this report. Findings described as confirmed are supported by the code paths, not by newly executed tests. Performance gains are hypotheses until measured.

The required implementation is C1, then C3, then C2. The code review supports that order: incomplete cumulative counters corrupt later deltas, mismatched baseline intervals corrupt raw latency, and bucket weighting must operate on correct raw latency. No performance or cleanup item is a prerequisite for these fixes.

The scope review found no runtime evidence justifying broader storage or API changes. Optional quick wins may be skipped without blocking completion. Stop after correctness and any selected small extras, review measurements, and create separate work only for a demonstrated problem.

## Scope classification

| Classification | Items | Decision |
| --- | --- | --- |
| Required now | C1, C3, C2 | Implement in that order with focused regression coverage. |
| Small optional experiment | P1 | Benchmark-gated; retain only for meaningful benefit at realistic sample counts. Does not block correctness delivery. |
| Opportunistic only | P2, D1, D3 | P2 only while already in `downsample.go`; D1/D3 only in naturally touched code or a separately scoped cleanup pass. No separate work items or decision checkpoints. |
| Optional / deferred correctness | C4 | Revisit only with evidence of an observable problem caused by inconsistent summary fields. No state-access consolidation now. |
| Measurement-only / deferred | P3, P4, P5 | No immediate implementation. Require realistic runtime evidence, attribution to the proposed hotspot, and a separate scoped follow-up. |
| Guidance only | D2, D4 | No wrapper/API cleanup sweep or generic abstraction work without a concrete maintenance problem. |

A shared SQLite connection and repeated SQL are structural observations, not proof of unacceptable overhead. Keep the current architecture unless measurements demonstrate a meaningful product impact.

## Source Constraints

- User request: quick, safe wins in polling/processing and dashboarding, serious correctness issues, dead code/duplication, and proposed solutions with sufficient direction for another model to implement.
- Scope refinement: correctness is primary; optional performance/cleanup work may be skipped. After the checkpoint, create P3/P4/P5 work only if runtime measurements demonstrate a real problem.
- Report only in this task. Implementation remains proposed; do not interpret these checkboxes as completed work.
- Follow `AGENTS.md` and `AGENTS.local.md`. Keep the binary/runtime footprint small and changes independently testable.
- Preserve raw poll snapshots and metric samples, query-time counter deltas, nonnegative displayed deltas, and visible collector warnings/errors.
- Keep Actuator details in collector code. No generic ingestion, derived delta tables, rollup tables, ORM, or expanded product scope.
- Preserve public JSON and configuration contracts unless a finding explicitly requires a documented behavior correction. Do not create commits without user approval after review.
- Planning structure follows the private repository's `docs/plans/0000-planning-principles.md`, located with `repo-map get statlite-private`.

## Critical paths and existing safeguards

**Polling:** `monitor.Monitor.PollNow` serializes collection per target, detects/touches an app run, saves a transaction containing the poll and samples/events, and reloads the latest snapshot before updating cached status. App-run updates are separate writes from snapshot persistence. The reread loads poll metadata, samples, events, and possibly process start time.

**Collection:** Spring Actuator mode performs sequential health/metric requests, with per-request timeout and bounded response bodies. `springPollSession` caches repeated endpoint fetches within a poll, including status 404 shared by the 404 and 4xx totals. Spring's scrape path and Quarkus use different collection paths; do not assume every target incurs the Actuator fan-out.

**Dashboard:** `dashboard.js:refresh` requests summary, series, and 20 recent events concurrently. `storage.Series` queries five counter baselines and all selected raw samples in the range, creates raw points, and only then does the server aggregate larger standard ranges. Summary includes cached target snapshots plus a restart-history query.

**Contention:** `storage.Open` sets `SetMaxOpenConns(1)`. Chart reads, poll writes, history enrichment, and retention share the connection. Frontend request concurrency does not create database execution concurrency. Retention deletes expired polls and cascading children in one transaction, with a 30-second cleanup timeout.

**Keep:** serialized target polls, transactional snapshot writes, bounded collector bodies, per-poll fetch reuse, restart-aware/nonnegative deltas, SQL event limits for the dashboard, stale-response cancellation and generation checks, and hidden-tab refresh suppression. These already address common failure/performance modes.

## Correctness findings

### C1. Partial status groups are persisted as complete cumulative counters

Priority: high. Confirmed code path. Area: `internal/collector/spring.go:collectHTTPStatusTotal`.

Fetch errors and missing COUNT measurements continue to the next status. If any other member succeeds, `sawStatus` causes the partial sum to be stored. A 4xx group containing 404 and 409 can therefore shrink when only 409 fails, then rebound on recovery. A sequence of complete 100, partial 40, complete 110 suppresses the negative delta but subsequently displays 70 instead of the real increase of 10 across the gap.

Proposed solution: treat a known group's aggregate as unavailable if any required member cannot be read or lacks a valid measurement. Preserve existing warnings and successful independent metrics. Preserve zero for genuinely empty groups. Do not replace an incomplete group with zero or carry forward a fabricated current sample.

Validation: use at least two statuses in the same group; fail one while the other succeeds, then recover. Assert the partial aggregate is omitted, warnings remain, and stored-series recovery uses the last complete baseline. Cover missing COUNT as well as HTTP failure. Existing `spring_contract_test.go` case `partial status failure` has a successful 4xx group and an entirely failed 5xx group; it does not exercise this within-group case.

### C2. Bucketed latency is an unweighted average of poll averages

Priority: high for dashboard accuracy. Confirmed code path. Area: `internal/storage/downsample.go:bucketAccumulator`.

The accumulator sums `AverageLatencySeconds` and divides by the number of non-null latency points. One request averaging 1 second and 99 requests averaging 0.1 seconds produce 0.55 seconds rather than the request-weighted 0.109 seconds.

Proposed solution: accumulate latency multiplied by its matching request delta and divide by the sum of those matching request deltas. Exclude points without valid latency or positive matching request counts from both numerator and denominator. Preserve null if no valid contributors exist. Complete C3 first so the inputs represent matching intervals. Keep request/error sums and gauge averages unchanged.

Validation: unequal request volumes, zero traffic, missing latency, restart boundaries, and sparse/native output. Update existing tests and comments that currently specify an arithmetic latency average; this is an intentional behavior correction, not merely a refactor.

### C3. Missing counter measurements can give latency mismatched intervals

Priority: high for intermittent metric gaps. Confirmed code path. Area: `internal/storage/series.go:counterValue`, `previousCounterValues`, and `buildSeriesPoint`.

Each counter retains its own last available baseline. If request count advances during a poll where total duration is missing, the next duration delta spans two intervals while the request delta spans one. Both are nonnegative and currently get divided.

Proposed solution: retain source poll identity with each counter baseline, including baselines loaded before the requested range. Calculate latency only when count and duration share a baseline poll and the existing app-run/reset checks pass. Continue advancing each raw counter independently for its own delta. Emit null for the mismatched latency interval and resume once the pair aligns; do not suppress unrelated request/error data.

Validation: missing duration, missing count, independent counter reset, and the same cases crossing the query's start boundary. Include a normal recovery poll proving latency resumes. No schema change should be necessary because source poll IDs already exist.

### C4. Cached status and snapshot are read separately

Classification: optional / deferred. Priority: low; transient consistency issue. No observable user impact was established by this review. Area: `internal/monitor/manager.go:Summaries` and `internal/server/api.go:handleSummary`.

`Status()` and `LatestSnapshot()` each lock independently. A successful poll between these calls can pair one poll's status/ID with another poll's snapshot. Summary also reads the selected target separately from its entry in `targets`. This is not a demonstrated data race, but can produce internally inconsistent JSON.

Revisit only if a reproduction or runtime observation links mixed summary fields to an observable problem. This is not part of the near-term effort. Possible future solution: expose a small combined monitor-state read under one read lock. Construct the selected target's summary fields from the same captured entry. Keep published snapshots immutable. Do not widen locks across JSON encoding or database queries.

Validation: repeatedly collect and read combined state, asserting stored poll ID and snapshot ID agree when a stored snapshot is present. Account for storage failures, which intentionally may leave the previous snapshot while reporting a failed attempt with no stored poll ID.

## Performance opportunities

### P1. Reuse sample INSERT preparation within each transaction

Classification: small optional optimization experiment, after correctness. `internal/storage/write.go:SaveCollectionResultWithAppRun` repeatedly executes identical SQL per sample, but that alone does not establish a meaningful cost. Benchmark the current path and a minimal comparison variant using realistic StatLite sample counts. Adopt transaction-local sample INSERT preparation only if the comparison shows a repeatable, meaningful benefit. Otherwise drop the variant and record the result. If retained, prepare once for a nonempty sample list, reuse it, and close it on success and failure. Event preparation is outside this experiment.

Preserve atomicity and descriptive metric-key errors. Measure representative normalized sample counts, not artificial thousands of samples. Compare time/allocations with an identical database setup, including representative file-backed SQLite commits; an in-memory microbenchmark alone is insufficient. Judge benefit by absolute cost per poll and expected target/poll volume as well as relative improvement, beyond run-to-run noise. Benefits may be modest because commits can dominate. Do not keep extra complexity for a tiny isolated SQL speedup, or build a broad benchmark framework for this experiment.

### P2. Bound aggregated output capacity by bucket count

Classification: opportunistic quick win only while already modifying `downsample.go`, for example during C2. Otherwise skipping it is entirely reasonable; do not enter the file just for this optimization. `AggregateSeries` allocates output capacity equal to the entire raw point count even when a 30-day view collapses to a few hundred buckets.

Estimate capacity conservatively from the range and bucket duration, including partial boundary buckets, and cap it at the number of input points. Handle empty and sparse series without changing the existing native-resolution behavior. Ordinary slice growth can handle underestimates. Validate output equivalence and compare allocations on a dense long range. The expected gain is allocation reduction only: this removes an oversized second allocation while the full raw series still gets materialized. Do not claim reduced raw-series scanning or database contention. No separate decision checkpoint is warranted.

### P3. Aggregate while building series points

Classification: deferred, outside the immediate implementation plan. P2 does not make P3 the next step. `Series` currently materializes all raw points before `handleSeries` aggregates them, but no runtime profile establishes material impact. Revisit only if profiling realistic long-range queries attributes meaningful memory or CPU cost to raw-series materialization. If justified, create a separate plan to feed computed, restart-aware points into a bucket accumulator as rows are consumed, keeping the raw latest point and current disk semantics.

Keep raw/custom and 1h behavior intact. Preserve stable UTC bucket boundaries, partial first bucket timestamps, null semantics, cutoff baseline clearing, and identity handling. In particular, current cutoff clearing occurs before aggregation; moving aggregation into storage must preserve that ordering rather than clearing a whole aggregated bucket. Do not aggregate cumulative counters before calculating deltas. Benchmark bounded output memory and note that scanning all selected raw rows still costs CPU and holds the SQLite connection.

### P4. Snapshot reread and separate app-run writes

Classification: deferred / premature optimization without measurements. `PollNow` rereads data it just wrote, and app-run touching adds a separate write before each successful snapshot transaction. Returning committed snapshot metadata from storage could avoid rereads; moving app-run touch into the poll transaction could reduce transaction overhead.

Do not change either path now. Revisit only when runtime measurements attribute meaningful polling/storage overhead to snapshot rereads or separate app-run writes, then scope a separate change. Preserve storage normalization, copied/immutable sample ownership, process-start representation, status/error derivation, rollback behavior, and exact saved poll identity. Avoid manually duplicating storage's snapshot construction rules in monitor code.

### P5. Retention and baseline query costs need measurements first

Classification: measurement/investigation only. Large retention transactions and five per-key baseline queries can occupy the shared connection; no resulting polling stall was measured in this review. Baseline lookups may search substantial history for absent keys. Inspect query plans and timings with long history, absent optional counters, and multiple targets before choosing an index or query rewrite.

Do not implement batching, new indexes/query rewrites, WAL, or additional SQLite connections from static inspection. Only if runtime measurements demonstrate a real problem should a separate plan evaluate alternatives. For example, if retention demonstrably stalls polling, that later plan could consider bounded transactional deletion batches with cancellation between batches. Preserve cutoff publication only after the intended cleanup succeeds. Neither batching nor a query rewrite belongs in the first quick-win slice. Increasing connection count/WAL settings also requires separate concurrency and SQLite configuration review.

## Dead code and duplication

### D1. Two strong dead-code removal candidates

Classification: opportunistic cleanup, conceptually alongside D3. Remove only if the code is naturally touched during this work or during a separately scoped cleanup pass. Do not deliberately enter unrelated files to remove these helpers. Rechecking Go references during this scope review still found definitions but no callers, including tests:

- `internal/collector/actuator.go:ActuatorClient.getJSON`: obsolete generic fetch/decode helper, duplicating parts of the active raw-response decoding path.
- `cmd/statlite/main.go:runInspect`: wrapper around `runInspectWithTyped`; command dispatch already calls the typed implementation.

Proposed solution: recheck references across the whole repository at implementation time, remove these helpers and newly unused imports, and run affected package tests plus a build. Do not remove active typed decoding, `setRaw`, or inspect dispatch. Expected gain is less code to maintain; no product or performance benefit has been established. This does not warrant a separate work item or decision checkpoint.

### D2. Guidance: test-only wrappers are not automatically dead code

Examples with no production name references outside their declarations include `server.parseRange`, `inspect.parseQuarkusEndpoint`, `collector.NewSpringActuatorCollector`, `storage.SaveCollectionResult`, and `prometheus.NewAccumulator`. Tests do use them. Several manager forwarding methods (`Monitor`, `Metadata`, `Status`, `LatestSnapshot`, `PollNow`, `Series`) also overlap the active resolve-target-then-monitor path; inspect each method's callers separately.

Classification: guidance only, no actionable cleanup sweep. Test-only use alone does not justify deleting or relocating wrappers, redirecting tests, or consolidating APIs. Revisit an individual wrapper only when a concrete maintenance problem establishes a benefit. Keep useful constructors and APIs when their clarity outweighs a few lines. Do not delete entire accumulator/storage implementations because one convenience entry point is test-only.

A textual reference count is only a candidate finder: `StatliteMetricsField.UnmarshalJSON`, for example, is invoked through `encoding/json` and must be retained despite having no explicit method call. The sweep was not whole-program static analysis or an exhaustive dead-code proof. No convincing dead dashboard function was identified in this bounded review.

### D3. Remove impossible error plumbing in the series flush closure

`internal/storage/series.go:Series` defines `flush` as returning an error but every path returns nil. Both callers branch on an error that cannot occur. Classification: opportunistic only. Make the closure return nothing and simplify callers only while already modifying `series.go`, such as during C3. Do not create a standalone task or use this cleanup to justify P3. This is a small readability cleanup with no behavior change; no standalone test is needed.

### D4. Guidance: avoid speculative consolidation

Classification: guidance only. Manager forwarding methods and repeated summary reads overlap, but this review establishes no maintenance problem requiring consolidation. C4 is deferred and does not justify a broader API cleanup. Source adapters also repeat some metric validation/mapping, while the dashboard maps points into multiple chart datasets. These serve distinct metrics and contracts; a shared framework or single generic chart builder is not justified by this review. Keep explicit mappings unless a measured hotspot or repeated bug provides a specific reason to change them.

## Proposed execution

Current phase: review complete, implementation proposed.
Current chunk: none started.
Blockers: none for report delivery. No implementation issue is assigned.

Work sequentially through the three required chunks. Before each implementation chunk, preview its intended change, files, verification, and risks. Split any chunk that exceeds a small independently testable change. P1 is an optional decision, not a delivery requirement: record skipped/dropped with a reason, and do not leave correctness delivery waiting for it. P2/D1/D3 are incidental opportunities only; skipping them requires no separate decision or justification. Do not expand the touched files to pursue cleanup, and do not perform a D2/D4 sweep.

- [ ] Required chunk 1: fix C1 with same-group failure/recovery regression coverage through collector and stored series.
- [ ] Required chunk 2: fix C3 with matching baseline identity, including pre-range history and recovery tests. D3 may be simplified here if convenient, without a separate cleanup task.
- [ ] Required chunk 3: fix C2 with request-weighted latency and null/zero/restart coverage.
- [ ] Optional decision 4: benchmark P1 if worthwhile. Adopt only for a repeatable, meaningful benefit at realistic sample counts; otherwise drop it or skip the experiment.
- [ ] Checkpoint 5: stop implementation, summarize correctness validation and optional outcomes, and review available runtime measurements. If further investigation is warranted, take a bounded realistic measurement before proposing more code. Record no further work when evidence does not show a material problem.

### Checkpoint and follow-up gate

Completion of the required work means C1/C3/C2 are fixed and their relevant checks pass. Optional items need not be implemented. Do not automatically roll into P3/P4/P5 or C4 after this checklist.

For any new P3/P4/P5 work, first record the realistic workload (history range, actual sample cardinality, polling interval, target count, and concurrent viewers), observed impact (memory, CPU, query latency, poll delay, or retention stall), and profiling/timing evidence attributing it to the proposed change. Compare the cost with the deployment's resource budget. A large synthetic input or a theoretical contention path alone is insufficient.

Create a separate scoped follow-up only if that evidence demonstrates a real problem. P3 needs raw-materialization attribution; P4 needs reread/app-run overhead attribution; P5 needs measured query or retention impact. The follow-up should select the smallest useful change and its acceptance criteria, not authorize all listed mechanisms. C4 separately requires observable consistency impact, rather than being bundled into performance work.

## Verification guidance

Use the `lite-tools` skill for test execution. For implementation, run the affected packages first, then the appropriate integration checks. For the required correctness chunks, start with `go-lite test ./internal/collector ./internal/storage` and relevant server integration tests for chart aggregation. If D1 is incidentally removed, run tests for the affected package(s). Monitor/inspect refactors and state-access race checks are not tasks in this near-term plan; add such checks only if a separately justified change touches those paths. Run `go build ./cmd/statlite` and `git diff --check` for cleanup.

If dashboard HTML, JS, or tests change, separately run `npm-lite node --test internal/dashboard/static/dashboard.test.js`. For visible chart behavior changes, manually check a dense long range and a sparse 1h range, missing metrics, target switching, and polling recovery. Go tests do not exercise dashboard Node tests.

If an optional experiment or follow-up investigation is selected, its benchmarks should report input size, polling interval, number of targets, SQLite setup, allocations, and wall time. Keep before/after conditions identical. Do not claim reduced database contention merely from a lower JSON size or lower output slice capacity.

## Outcome

Delivered: source-review report, revised after code reinspection to prioritize C1/C3/C2, make small optimizations and cleanup optional, and gate broader work on runtime evidence. No code evidence required elevating a deferred item above the correctness fixes.

Intentionally skipped: application edits, new tests, benchmark execution, broad refactoring, dependency updates, schema changes, and commits. Implementation checkboxes remain open.
