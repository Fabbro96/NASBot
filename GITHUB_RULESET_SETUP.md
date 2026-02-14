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
   - **Require approvals** = `1`
   - **Dismiss stale pull request approvals when new commits are pushed**
   - **Require review from code owners**
   - **Require conversation resolution before merging**
   - **Require status checks to pass before merging**
   - **Require branches to be up to date before merging**
   - **Require linear history**
   - **Block force pushes**
   - **Block deletions**
   - **Do not bypass for administrators** (recommended)
7. In required status checks, select exactly:
   - `Secret Scan`
   - `Build & Test (Go 1.22.x)`
   - `Build & Test (Go 1.23.x)`
   - `Dependency Review`
   - `CodeQL Analysis (go)`
8. Save ruleset.

## 2) Tag Ruleset for Releases (`v*`)

1. In **Settings** → **Rules** → **Rulesets** click **New ruleset** → **New tag ruleset**.
2. Name: `release-tags-protection`.
3. Target tags pattern: `v*`.
4. Enable:
   - Restrict creation/update to maintainers
   - Block deletion for non-admin users
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
- Security policy reference: `SECURITY.md`.
- Ruleset templates are versioned in `.github/rulesets/`.
