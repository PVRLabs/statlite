# StatLite v0.5.0 Release Journal

## Release gate

- Release version: `v0.5.0`
- Release date: 2026-09-25
- Release issue: not recorded
- Candidate branch: `main`
- Candidate commit: `48827b2d5d5694786605c6d820e507c5afdf229b`
- Final tagged commit: `48827b2d5d5694786605c6d820e507c5afdf229b`
- Post-release development bump: `689e3d475b5ac5d6a2ad4e9da0dfb86158bfc500`
- Reviewer/operator: release operator

### Goal and user-facing scope

Ship the v0.5.0 changes described in the [public changelog](../CHANGELOG.md),
including the deprecation guidance updated during release preparation.

### Certification matrix

| Suite | Disposition | Result |
| --- | --- | --- |
| Spring contract | Required | PASS after publication; four cases recorded in private certification results. |
| Quarkus contract | Required | PASS after publication. |
| Historical database upgrade | Required | PASS after publication. |
| Stress/dashboard query | Required | PASS after publication with rebased representative ranges. |
| Manual product smoke | Required | PASS after publication: exact candidate version, `/healthz`, `/statlite/metrics`, and self-monitoring checked. |

## Candidate preparation

- Prepare result: PASS on the release candidate.
- Candidate binary: `/private/tmp/statlite-0.5.0`
- Candidate SHA-256: `e35bbbafafaf7d66d94e2ba5ed257848c04a4e04638fca080aff5e8dd3d4a965`
- Prepare manifest: `/private/tmp/statlite-0.5.0-prepare.json`
- Pull-request/test CI: PASS, run `36169869214`.
- Public integration certification CI: PASS, run `36169869156`.

## Private certification and manual review

Private certification was mistakenly omitted before publication, despite the
private release checklist requiring it. After publication, all required
private suites were run against the exact release candidate commit and binary;
all passed. The results and raw reports are indexed in
[`statlite-private/certification/RESULTS.md`](../../statlite-private/certification/RESULTS.md).
This retrospective evidence does
not change the fact that the pre-publication gate was missed.

- Spring Boot 3.5.5 and Boot 4.0.8 Actuator/Prometheus plus deprecated config: PASS.
- Quarkus 3.39.1: PASS.
- Historical v1-to-v2 database upgrade: PASS.
- Stress/dashboard: PASS; representative ranges present, no HTTP failures,
  7d series max 0.715s, 30d series max 3.557s, peak RSS 125.12 MiB.
- Browser dashboard: PASS for target/range switching, three targets including
  `statlite-self`, and browser error checks.
- Exact candidate smoke: PASS; `/healthz` reported v0.5.0 and storage ready;
  `/statlite/metrics` returned the `statlite-metrics/v1` profile.

The stress fixture generator was updated to write the current schema-v2
provenance fields, and the browser harness selector was updated to the current
dashboard health element. These private harness changes remain uncommitted in
the private repository working tree.

## Release binding and publication

- Tag identity: `v0.5.0` resolves to the final tagged commit above.
- GitHub release workflow: PASS, run `36170043363`.
- GitHub release: [StatLite v0.5.0](https://github.com/PVRLabs/statlite/releases/tag/v0.5.0).
- Release archives, checksums, container manifests, and release-container
  smoke checks: PASS in the release workflow.
- Post-release development bump: PASS; CI run `36170521327`.
- Homebrew update/install verification: PASS; workflow run `36170695288`,
  including formula audit, upgrade from 0.4.3, brew test, and installed version.
- Homebrew verification script: PASS for run `36170695288` and `v0.5.0`.
- Release announcement: pending.

## Blockers, limitations, and accepted risks

- Process deviation: private certification was not completed before tagging and
  publication. All private suites later passed on the exact tagged candidate;
  this is recorded as retrospective evidence, not a pre-release approval.
- The release issue and announcement URL were not recorded here.

## Evidence and sign-off

- Reviewer and date: release operator, 2026-09-25
- Private suite reports: see [`statlite-private/certification/RESULTS.md`](../../statlite-private/certification/RESULTS.md).
- Main post-release CI run: `36170521327` (PASS).
- Release workflow: `36170043363` (PASS).
- Homebrew update workflow: `36170695288` (PASS).
