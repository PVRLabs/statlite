# StatLite v0.4.3 Release Journal

## Release gate

- Release version: `v0.4.3`
- Release date started: 2026-09-22
- Release issue: pending remote GitHub access
- Candidate branch: `main`
- Candidate commit: pending release-preparation commit
- Final tagged commit: pending
- Reviewer/operator: pending

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
- Prepare result: pending
- Candidate binary: pending
- Candidate SHA-256: pending
- Prepare manifest: pending
- Generated release notes: pending review
- Candidate branch and commit: pending

## Certification

- Required suites: pending
- Accepted reports: pending review
- Public integration CI: pending remote CI result

## Manual product review

- Exact candidate `--version`: pending
- `/healthz`: pending
- Dashboard and browser console: pending
- `/statlite/metrics` and self-monitoring: pending
- Integration inspection/configuration review: pending

## Release binding and publication

- Final tagged commit: pending
- GitHub release workflow: pending
- GitHub release asset verification: pending
- Published GHCR images: pending
- Post-release development bump: pending
- Homebrew tap and install verification: pending
- Release announcement: pending

## Blockers, limitations, and accepted risks

- GitHub CLI authentication is currently invalid and GitHub API access is
  unavailable from this workspace. Release issue, branch push, CI verification,
  tag creation, publication, and post-release checks remain pending.
- No technical risk has been accepted yet.

## Evidence and sign-off

- Reviewer and date: pending
