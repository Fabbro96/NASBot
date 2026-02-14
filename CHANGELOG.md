# Changelog

All notable changes to this project are documented in this file.

## v0.1.1 - 2026-02-14

### Added
- Hardened CI quality gate script (`scripts/ci_guard.sh`) used by CI and release workflows.
- Versioned GitHub ruleset templates for branch/tag protections under `.github/rulesets/`.
- Ruleset automation script (`scripts/apply_github_rulesets.sh`) with create/update and dry-run modes.
- CODEOWNERS baseline for repository and security-critical paths.
- Operational rollout guide for rulesets (`GITHUB_RULESET_SETUP.md`).

### Changed
- CI workflow now uses a unified quality gate with timeout guards.
- Release workflow now validates semantic tag and changelog presence before publishing assets.
- Security policy and branch protection docs now include automation and enforcement guidance.

### Validation
- Quality gate passing (`scripts/ci_guard.sh`).
- Tests passing (`go test ./...`).
- Build passing (`go build ./...`).
- Release binaries built successfully (`./build_release.sh`).

## v0.1.0 - 2026-02-14

### Added
- Network watchdog forced reboot on prolonged downtime (configurable threshold in minutes).
- Manual forced reboot command/button without interactive confirmation.
- Extended language support: English, Italian, Spanish, German, Chinese, Ukrainian.
- Automatic translation key coverage sync from English fallback.
- Legacy config auto-heal/merge for missing fields using default templates.
- New tests for system commands, network watchdog, language callbacks, config defaults, translation coverage.

### Changed
- Refactored callback/settings handlers into focused modules.
- Split monitor management into dedicated files (`manager`, `runtime`, `raid`, `stress`).
- Split reports into runtime/schedule/AI focused modules.
- Centralized translation runtime helpers in dedicated module.
- Hardened config sanitization and defaults for new watchdog settings.

### Validation
- Test suite passing (`go test ./...`).
- Build passing (`go build ./...`).
- Release binaries built successfully (`./build_release.sh`).
