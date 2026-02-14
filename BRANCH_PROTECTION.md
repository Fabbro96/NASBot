# Branch Protection Baseline (main)

This document defines the mandatory GitHub protection settings for `main`.

## Target Branch
- Branch name pattern: `main`

## Required Pull Request Rules
- Require a pull request before merging: **ON**
- Require approvals: **1** minimum
- Dismiss stale approvals when new commits are pushed: **ON**
- Require review from code owners: **ON** (`.github/CODEOWNERS` now present)
- Require conversation resolution before merging: **ON**

## Required Status Checks
Enable **Require status checks to pass before merging** and select these checks:
- `Secret Scan`
- `Build & Test (Go 1.22.x)`
- `Build & Test (Go 1.23.x)`
- `Dependency Review`
- `CodeQL Analysis`

Also enable:
- Require branches to be up to date before merging: **ON**

## Additional Protections
- Require linear history: **ON**
- Require signed commits: **ON** (recommended)
- Include administrators: **ON**
- Restrict force pushes: **ON**
- Restrict deletions: **ON**

## Release Tag Protection
Create a tag ruleset for `v*`:
- Restrict tag creation/update to maintainers only
- Block tag deletion for non-admin users

## Secrets & Environments
- Keep release job on default environment unless approvals are desired.
- If approvals are desired, create environment `release` and require reviewer approval.

## Setup Steps (GitHub UI)
1. Repository `Settings` → `Rules` → `Rulesets`.
2. Add branch ruleset for `main` with settings above.
3. Add tag ruleset for `v*` with protection above.
4. Save and verify by opening a PR from a test branch.

## Verification Checklist
- A PR without passing checks cannot merge.
- A PR without approval cannot merge.
- Direct push to `main` is blocked for non-admins.
- Creating non-compliant `v*` tags is blocked.
