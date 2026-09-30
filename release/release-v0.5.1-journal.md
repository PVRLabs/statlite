# StatLite v0.5.1 Release Journal

## Release gate

- Release version: `v0.5.1`
- Release date: 2026-09-30
- Release issue: [#57](https://github.com/paseo-verde-research/statlite/issues/57)
- Candidate branch: `main`
- Candidate commit: pending
- Final tagged commit: pending
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

| Suite | Disposition | Reason / result |
| --- | --- | --- |
| Spring contract | Required | Pending |
| Quarkus contract | Required | Pending |
| Historical database upgrade | Required | Pending |
| Stress/dashboard query | Required | Required; use `--rebase` and accept only representative ranges |
| Manual product smoke | Required | Pending |

## Candidate preparation

- Prepare result: pending.
- Candidate binary and SHA-256: pending.
- Prepare manifest: pending.
- Pull-request/test CI: pending.
- Candidate diff and generated release notes review: pending.

## Private certification and manual review

- Spring contract: pending.
- Quarkus contract: pending.
- Historical database upgrade: pending.
- Stress/dashboard query: pending.
- Dashboard browser review: pending.
- Exact candidate smoke, health, self-metrics, and configuration output: pending.
- Manual reviewer and date: pending.

## Release binding and publication

- Final approved commit and tag identity: pending.
- Main CI: pending.
- GitHub release and archive verification: pending.
- GHCR publication and smoke checks: pending.
- Post-release development version and CI: pending.
- Homebrew workflow and installation verification: pending.
- Release announcement URL and date: pending.

## Blockers, limitations, and accepted risks

- Pending certification and publication.

## Evidence and sign-off

- Accepted certification reports: pending.
- Release journal: this file.
- Pull-request and main CI runs: pending.
- Reviewer and date: pending.
