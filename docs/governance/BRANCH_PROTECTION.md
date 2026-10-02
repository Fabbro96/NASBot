# Branch Protection Baseline (main)

This document defines the mandatory GitHub protection settings for `main`.

The source of truth is `.github/rulesets/main-protection.json`; this file describes it.
Where the two disagree, the ruleset wins and this document is the bug.

## Target Branch
- Branch name pattern: `main`

## Required Pull Request Rules
- Require a pull request before merging: **ON**
- Require approvals: **0** minimum (`required_approving_review_count: 0`)
- Dismiss stale approvals when new commits are pushed: **ON**
- Require review from code owners: **OFF** (`require_code_owner_review: false`),
  even though `.github/CODEOWNERS` exists
- Require conversation resolution before merging: **ON**

## Required Status Checks
Enable **Require status checks to pass before merging** and select exactly these seven.
Each name is the literal `name:` of a workflow job — a required context that no job ever
reports blocks every pull request indefinitely, so a rename on either side is a breaking
change:

- `Secret Scan` — `.github/workflows/ci.yml`
- `ShellCheck` — `.github/workflows/ci.yml`
- `Build & Test (Go 1.22.x)` — `.github/workflows/ci.yml` (matrix)
- `Build & Test (Go 1.23.x)` — `.github/workflows/ci.yml` (matrix)
- `Deadlock & Race (Go 1.23.x)` — `.github/workflows/deadlock-race-gate.yml`
- `Dependency Review` — `.github/workflows/security.yml`
- `CodeQL Analysis` — `.github/workflows/security.yml`

Also note:
- **Require branches to be up to date before merging: OFF**
  (`strict_required_status_checks_policy: false`). The ruleset does not force a branch
  update, so a green run stays valid even if the base moves.

One job is **not** required and is therefore advisory only:
`Go Vulnerability Check` (`govulncheck`). Note also that the CI workflow itself skips
`**/*.md` on `push` (but not on `pull_request`), so a push that changes only
documentation reports no CI contexts at all — which is why the ruleset cannot be
evaluated on such a push.

## Additional Protections
- Require linear history: **ON** (`required_linear_history`)
- Require signed commits: **OFF** — the ruleset has no `required_signatures` rule.
  Signed commits are still recommended locally, they are not enforced on `main`.
- Include administrators: **ON** — `bypass_actors` is empty, so no actor, admin
  included, bypasses the ruleset.
- Restrict force pushes: **ON** (`non_fast_forward`)
- Restrict deletions: **ON** (`deletion`)

## Release Tag Protection
Create a tag ruleset for `v*` (`.github/rulesets/release-tags-protection.json`):
- Target tag pattern: `refs/tags/v*`
- Restrict tag update: **ON** (`update`)
- Block tag deletion: **ON** (`deletion`)

## Secrets & Environments
- Keep release job on default environment unless approvals are desired.
- If approvals are desired, create environment `release` and require reviewer approval.

## Setup Steps (GitHub UI)
1. Repository `Settings` → `Rules` → `Rulesets`.
2. Add branch ruleset for `main` with settings above.
3. Add tag ruleset for `v*` with protection above.
4. Save and verify by opening a PR from a test branch.

Detailed click-by-click guide: [GITHUB_RULESET_SETUP.md](GITHUB_RULESET_SETUP.md).

Automated apply/update: `scripts/apply_github_rulesets.sh`.

## Why the pull request rules are as weak as they are
Approvals `0` and code-owner review OFF do not protect `main` on their own; the seven
required checks do. They also mean **a maintainer can merge their own PR without a
second pair of eyes**, so treat the checks as the real gate and review as a habit, not
as an enforced control. Raising `required_approving_review_count` to 1 and turning
`require_code_owner_review` back on is a one-line ruleset change if that trade-off is
ever revisited.

## Verification Checklist
- A PR without passing checks cannot merge.
- Direct push to `main` is blocked for non-admins.
- Creating or deleting a non-compliant `v*` tag is blocked.
- The seven required check names appear verbatim in the PR's checks list: a context that
  never reports looks identical to one that is still pending, so read the list after the
  first push of a workflow change.
- `gh ruleset check` (or the Rulesets page) reports the ruleset as active.
