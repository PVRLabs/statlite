# StatLite v0.4.3 Release Journal

## Release gate

- Release version: `v0.4.3`
- Release date started: 2026-09-22
- Release issue: not recorded
- Candidate branch: `main`
- Candidate commit: `862dbd4aa010e15a184b119a4bc821e3bd8e7c9e`
- Final tagged commit: `862dbd4aa010e15a184b119a4bc821e3bd8e7c9e`
- Reviewer/operator: release operator, user-approved

### Goal and user-facing scope

Release StatLite v0.4.3 with first-class integration guidance and runnable
StatLite Metrics v1 examples for FastAPI, Django, Express, Go `net/http`, and
Gin; public integration certification journeys; the direct-v1 runtime
monitoring baseline; and the SQLite configuration-path correction.

### Certification matrix

| Suite | Disposition | Reason |
| --- | --- | --- |
| Spring contract | Required | Preserve the existing supported integration contract while the shared configuration and integration surface changes. |
| Quarkus contract | Required | Preserve the existing supported integration contract and release-wide integration CI coverage. |
| Historical database upgrade | Required | SQLite path resolution and historical data behavior are part of the release scope. |
| Stress/dashboard query | Required | The release includes dashboard and runtime-monitoring changes that should be checked against stored history. |
| Manual product smoke | Required | The release adds user-facing integration guidance, examples, and dashboard-facing behavior. |
| Public integration CI | Required | FastAPI, Express, Django, Go `net/http`, Gin, Spring, and Quarkus journeys are part of the release scope. |

### Compatibility promises and intentional limitations

- Existing Spring, Quarkus, and StatLite self-monitoring configurations remain
  supported.
- The new direct-v1 examples are process-local integrations. Load-balanced
  multi-process and replica deployments remain outside their supported model.
- Go heap usage is allocated runtime heap, not process RSS, container memory,
  a memory limit, or a maximum heap value. Go CPU is a best-effort runtime
  estimate, not exact OS process accounting.
- Windows release artifacts remain out of scope. Release targets remain Darwin
  and Linux on amd64 and arm64.

## Candidate preparation

- Prepare command: `python3 statlite-private/scripts/release.py prepare v0.4.3`
- Prepare result: PASS for the tagged candidate
- Candidate binary: `/private/tmp/statlite-0.4.3`
- Candidate SHA-256: `f63b3f0bc8b9b1e59c4ef6178c0a0906171b47dba27f7eaa05542e7734590c10`
- Prepare manifest: `/private/tmp/statlite-0.4.3-prepare.json`
- Generated release notes: PASS; published by the release workflow
- Candidate branch and commit: `main` at `862dbd4aa010e15a184b119a4bc821e3bd8e7c9e`

## Certification

- Required suites: carried forward from the complete `0ad75e9` certification matrix
- Accepted reports: Spring Boot, Quarkus, historical upgrade, rebased stress,
  and dashboard browser reports indexed in `statlite-private/certification/RESULTS.md`
- Public integration CI: PASS in runs `35757311609` and `35757311653`
- Carry-forward decision: the final public changes only finalized the checked-in
  version and dated changelog heading. The operator accepted skipping a second
  private certification run; the final candidate was independently prepared and
  release CI passed.

## Manual product review

- Exact candidate `--version`: PASS, `statlite v0.4.3`
- `/healthz`: PASS, version and storage status verified
- Dashboard and browser console: PASS, dashboard title and browser certification verified
- `/statlite/metrics` and self-monitoring: PASS, `statlite-metrics/v1` verified
- Integration inspection/configuration review: PASS in public integration CI

## Release binding and publication

- Final tagged commit: PASS, `v0.4.3` resolves to `862dbd4aa010e15a184b119a4bc821e3bd8e7c9e`
- GitHub release workflow: PASS, run `35757499873`
- GitHub release asset verification: PASS, [GitHub Release](https://github.com/PVRLabs/statlite/releases/tag/v0.4.3)
- Published GHCR images: PASS; multi-platform manifests, versions, and release-container smoke test passed
- Post-release development bump: prepared locally as `v0.4.4-dev`; CI and push pending
- Homebrew tap and install verification: pending
- Release announcement: pending

## Blockers, limitations, and accepted risks

- Private certification was not rerun against the final tagged commit by
  explicit operator decision. The accepted reports cover the preceding exact
  release-style candidate; the intervening public changes were limited to
  release metadata, and the final candidate passed prepare, public integration,
  archive, asset, image, and container checks.
- The ordinary non-rebased stress report with empty short ranges was not
  accepted. The rebased report was accepted, and the certification instructions
  now require checking the cached database age and representative-range field.

## Evidence and sign-off

- Reviewer and date: release operator, 2026-09-22
