# Security & Hardening Policy

## Scope
This policy applies to local development, pull requests, and releases.

## Secret Management
- Never commit real secrets (Telegram token, Gemini API key, credentials).
- Use `config.example.json` as the only committed template.
- Keep real values only in local `config.json` (already gitignored).
- If a secret leaks, rotate it immediately and invalidate old tokens/keys.

## Commit Guardrails
- A pre-commit hook blocks staged changes containing likely secrets.
- The scanner checks:
  - `bot_token` values
  - `gemini_api_key` values
  - Telegram-like token patterns
  - Generic Google API key patterns (`AIza...`)

## Branch & Release Rules
- Before merge/release, run:
  - `go test ./...`
  - `go build ./...`
  - `./build_release.sh`
- Create annotated tags only from validated commits.
- CI enforces: secret scan + format/vet/test/build gates.
- Security workflow enforces: Dependency Review + CodeQL analysis.
- Branch protection baseline: see [BRANCH_PROTECTION.md](BRANCH_PROTECTION.md).

## Incident Response (Leak)
1. Rotate leaked secrets immediately.
2. Remove secret from tracked history if present.
3. Revoke affected bot/API access.
4. Publish a short post-incident note in changelog/release notes.

## Local Setup (Required)
```bash
git config core.hooksPath .githooks
chmod +x .githooks/pre-commit scripts/secret_scan.sh
```
