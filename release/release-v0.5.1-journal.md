# StatLite v0.5.1 Release Journal

## Release gate

- Release version: `v0.5.1`
- Release date: 2026-09-30
- Release issue: [#57](https://github.com/paseo-verde-research/statlite/issues/57)
- Candidate branch: `main`
- Candidate commit: `ef154ea882647faa7f7593eb0097bc99e319df77`
- Final tagged commit: `ef154ea882647faa7f7593eb0097bc99e319df77`
- Reviewer/operator: release operator

### Goal and user-facing scope

Ship the v0.5.1 changes in the [public changelog](../CHANGELOG.md): bounded
sampling for 7-day and 30-day dashboard charts, the `--raw-series` option for
full-resolution troubleshooting, safe `statlite inspect` configuration
creation and append modes, and a Simplified Chinese README landing page.

### Compatibility promises and limitations

- Preserve existing configuration and inspection behavior. Plain
  `statlite inspect` remains read-only; the new write modes require an explicit
  destination path.
- Dashboard overview charts use bounded sampling and can omit intermediate
  spikes or counter resets. Stored history remains authoritative, and
  `--raw-series` returns full-resolution data within the effective requested
  range without dashboard sampling or aggregation.
- No schema migration or configuration migration is part of this release.

### Certification matrix

| Suite | Disposition | Result |
| --- | --- | --- |
| Spring contract | Required | PASS: Boot 3.5.5 and Boot 4.0.8 Actuator/Prometheus, including deprecated config compatibility. |
| Quarkus contract | Required | PASS: Quarkus 3.39.1, Java 21. |
| Historical database upgrade | Required | PASS: v0.2.2 SQLite fixture, migration and historical API checks. |
| Stress/dashboard query | Required | PASS: `--rebase`; representative ranges yes; 2,592,000 polls; no HTTP failures. |
| Manual product smoke | Required | PASS: exact candidate version, health, self-metrics, inspection/config output, and visual review. |

## Candidate preparation

- Prepare result: PASS on release candidate `ef154ea`.
- Candidate binary: `/private/tmp/statlite-0.5.1`.
- Candidate SHA-256: `b1f0d368f74c7b88b08c782c74e7a30e537b16432a180341f67cc27a09c08880`.
- Prepare manifest: `/private/tmp/statlite-0.5.1-prepare.json`.
- Generated release notes: `/private/tmp/statlite-0.5.1-release-notes.md`, reviewed against the changelog and `v0.5.0...v0.5.1` range.
- Candidate review: complete `v0.5.0..ef154ea` diff and user-facing claims reviewed.
- Pull-request CI: not applicable; candidate was committed directly to `main`.
- Main test CI: PASS, run `36774982209` on `ef154ea`.

## Private certification and manual review

All certification reports identify candidate commit
`ef154ea882647faa7f7593eb0097bc99e319df77` and binary SHA-256
`b1f0d368f74c7b88b08c782c74e7a30e537b16432a180341f67cc27a09c08880`. The
private worktree became dirty only because earlier reports from this matrix
were untracked while later suites ran. Reports and results index were committed
and pushed to private commit `b3d1bd3`.

- Spring contract: PASS, four cases across Boot 3.5.5 and Boot 4.0.8.
- Quarkus contract: PASS, Quarkus 3.39.1 on Java 21.
- Historical database upgrade: PASS, v1-to-v2 migration, history preservation,
  append/restart, and SQLite integrity.
- Stress/dashboard query: PASS, rebased ephemeral copy; representative ranges
  yes; 7d warm series max 0.067 s, 30d max 0.197 s; peak RSS 27.29 MiB; cached
  source fixture preserved.
- Dashboard browser certification: PASS, target/range navigation, self-host
  metrics, and no browser errors.
- Exact candidate smoke: `/private/tmp/statlite-0.5.1 --version` returned
  `statlite v0.5.1`; `/healthz` returned status `ok`, version `v0.5.1`, and
  storage `ok`; `/statlite/metrics` returned `statlite-metrics/v1` with
  database, process, and host metrics.
- Inspection/config review: plain inspection produced the direct-v1 target;
  `--create-config` wrote an explicit temporary destination; duplicate target
  append was rejected; a distinct target append succeeded. Quarkus typed and
  partial inspection and deprecated Spring config were covered by reports.
- Presentation dashboard visual review: exact candidate and untouched
  presentation fixture, `payments-api-prod` at 1h and all targets at 1h, 24h,
  7d, and 30d. Reviewed at 1280x720 and 360x900; no horizontal overflow or
  console errors. Screenshot target/range/viewport: `payments-api-prod`, 1h,
  1280x720 and 360x900.
- Restart-boundary visual review: untouched Spring/Quarkus experiment fixture,
  `spring-stars`, 1h at 1280x900. Restart status and chart boundary were
  visible; no browser errors.
- Documentation review: install, Docker, configuration, integration,
  low-resource, example, and README claims checked against the final changes.
- Review date: 2026-09-30.

Accepted reports are indexed in
[`statlite-private/certification/RESULTS.md`](../../statlite-private/certification/RESULTS.md).

## Release binding and publication

- Final approved commit and tag: `ef154ea882647faa7f7593eb0097bc99e319df77`;
  `v0.5.1` resolves to this commit.
- Release workflow: PASS, run `36785875739`.
- GitHub release: [StatLite v0.5.1](https://github.com/PVRLabs/statlite/releases/tag/v0.5.1).
- Release archives and SHA-256 checksums: PASS; all four platform archives
  verified.
- GHCR: PASS; linux/amd64 and linux/arm64 manifests and image version verified;
  published container passed readiness, self-metrics schema, and dashboard
  smoke checks.
- Independent release asset verification: PASS via
  `statlite-private/scripts/release.py verify v0.5.1 --candidate-commit ef154ea882647faa7f7593eb0097bc99e319df77`.
- Post-release development version and CI: pending.
- Homebrew updater: PASS, run `36787181229`.
- Homebrew verification: PASS; formula audit, upgrade from 0.5.0, `brew test`,
  and installed version `statlite v0.5.1` all passed. Homebrew identified this
  Intel macOS host as Tier 3 and built the formula from source.
- Release announcement URL and date: pending.

## Blockers, limitations, and accepted risks

- No release blockers remain.
- The documented 7d/30d sampling is approximate and can omit intermediate
  spikes or counter resets; raw stored samples remain authoritative.

## Evidence and sign-off

- Release issue: [#57](https://github.com/paseo-verde-research/statlite/issues/57).
- Candidate binary and SHA-256: `/private/tmp/statlite-0.5.1`,
  `b1f0d368f74c7b88b08c782c74e7a30e537b16432a180341f67cc27a09c08880`.
- Prepare manifest: `/private/tmp/statlite-0.5.1-prepare.json`.
- Candidate/final tagged commit: `ef154ea882647faa7f7593eb0097bc99e319df77`.
- Main CI: `36774982209`; release workflow: `36785875739`.
- Homebrew updater: `36787181229`.
- Reviewer and date: release operator, 2026-09-30.
