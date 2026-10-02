# GitHub Ruleset Setup (Click-by-Click)

Use this guide to enforce protections in GitHub UI.

## Automated rollout (recommended)

Use the repository script to create/update rulesets via GitHub API:

```bash
chmod +x scripts/apply_github_rulesets.sh
scripts/apply_github_rulesets.sh --repo Fabbro96/NASBot
```

Preview without changes:

```bash
scripts/apply_github_rulesets.sh --repo Fabbro96/NASBot --dry-run
```

## 1) Branch Ruleset for `main`

1. Open repository **Settings**.
2. Go to **Rules** → **Rulesets**.
3. Click **New ruleset** → **New branch ruleset**.
4. Name: `main-protection`.
5. Target branches: `main`.
6. Enable:
   - **Require a pull request before merging**
   - **Require approvals** = `0`
   - **Dismiss stale pull request approvals when new commits are pushed**
   - **Require conversation resolution before merging**
   - **Require status checks to pass before merging**
   - **Require linear history**
   - **Block force pushes**
   - **Block deletions**
   - **Do not bypass for administrators**
   Leave **Require review from code owners** off and **Require branches to be up to
   date** off: that is what `.github/rulesets/main-protection.json` sets
   (`require_code_owner_review: false`, `strict_required_status_checks_policy: false`).
7. In required status checks, select exactly these seven, spelled as they appear in the
   workflow `name:` fields:
   - `Secret Scan`
   - `ShellCheck`
   - `Build & Test (Go 1.22.x)`
   - `Build & Test (Go 1.23.x)`
   - `Deadlock & Race (Go 1.23.x)`
   - `Dependency Review`
   - `CodeQL Analysis`
8. Save ruleset.

> [!WARNING]
> A required context that no job ever reports blocks every pull request indefinitely.
> Names must match the `name:` of the job character for character: `CodeQL Analysis`
> and `CodeQL Analysis (go)` are not the same check. After renaming a job or adding a
> workflow, open a PR and confirm the new context actually appears in the checks list.

Jobs that are intentionally left out of the required list, and are therefore advisory:
`Go Vulnerability Check` (govulncheck).

## 2) Tag Ruleset for Releases (`v*`)

1. In **Settings** → **Rules** → **Rulesets** click **New ruleset** → **New tag ruleset**.
2. Name: `release-tags-protection`.
3. Target tag pattern: `refs/tags/v*`.
4. Enable:
   - **Restrict updates** (the `update` rule: tags can only be moved by an actor that
     bypasses the ruleset, and `bypass_actors` is empty)
   - **Block deletions**
5. Save ruleset.

## 3) Optional Release Environment Approval

1. Go to **Settings** → **Environments**.
2. Create environment `release`.
3. Add required reviewers (maintainers).
4. If used, update release workflow job to target that environment.

## 4) Verification (2 minutes)

1. Open a PR with a small change.
2. Confirm all required checks run and are marked required.
3. Try merging without approval/checks: merge must be blocked.
4. Try direct push to `main` as non-admin: must be blocked.
5. Try creating/deleting a `v*` tag without permission: must be blocked.

## Notes
- `CODEOWNERS` is already present in `.github/CODEOWNERS`.
- Baseline policy reference: `BRANCH_PROTECTION.md`.
- Security policy reference: `../SECURITY.md`.
- Ruleset templates are versioned in `.github/rulesets/`.
