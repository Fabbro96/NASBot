# Changelog

All notable changes to this project are documented in this file.

## Unreleased

### Added
- **Disk & Mount Point Watchdog:** Added real-time disk mount monitoring (`internal/app/monitor_disks.go`) to detect added, removed, unmounted disks, and I/O errors.
- **Docker Ghost Mount Detection:** Automatically detects disk reconnections/device node changes (e.g. `sda1` -> `sdb1`) and warns when running Docker containers (Plex, Bazarr, FileBrowser) hold stale mount references.
- **Container Impact Inspection:** Identifies running containers mapping affected mount points and provides Telegram action buttons to restart containers.
- **UI/UX & Status Refactoring:** Restructured `/status` output with clean section layout, consistent storage formatting, safe Markdown escaping, and eliminated stray newlines (`\n`).
- **Translations:** Added new disk monitoring keys across all 6 languages (`it`, `en`, `es`, `de`, `zh`, `uk`) with 100% test coverage.

## v1.5.0 - 2026-06-20

### Added
- **Docker Readiness:** Provided `Dockerfile` (Alpine-based) and `docker-compose.yml` for seamless containerized deployments.
- **Testing:** Achieved comprehensive test coverage with 30+ new unit and integration tests across application logic, commands, configurations, and utilities.
- **S.M.A.R.T. Monitoring:** Refactored HDD SMART monitoring to use thread-safe `SmartCache` in `MonitorState`, optimizing system calls.
- **CI/CD:** Documented `govulncheck` and `deadlock-race-gate.yml` in CI/CD pipeline.
- **Documentation:** General cleanup and translation of command descriptions to English.
- **Configuration:** Updated structure to support robust `SecondaryDisks` mapping.

### Validation
- Quality gate passing (`scripts/ci_guard.sh`).
- Tests passing (`go test ./... -v`).
- Docker deployment verified.

## v0.1.1 - 2026-02-14

### Added
- Hardened CI quality gate script (`scripts/ci_guard.sh`) used by CI and release workflows.
- Versioned GitHub ruleset templates for branch/tag protections under `.github/rulesets/`.
- Ruleset automation script (`scripts/apply_github_rulesets.sh`) with create/update and dry-run modes.
- CODEOWNERS baseline for repository and security-critical paths.
- Operational rollout guide for rulesets (`docs/governance/GITHUB_RULESET_SETUP.md`).

### Changed
- CI workflow now uses a unified quality gate with timeout guards.
- Release workflow now validates semantic tag and changelog presence before publishing assets.
- Security policy and branch protection docs now include automation and enforcement guidance.

### Validation
- Quality gate passing (`scripts/ci_guard.sh`).
- Tests passing (`go test ./...`).
- Build passing (`go build ./...`).
- Release binaries built successfully (`./scripts/build_release.sh`).

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
- Release binaries built successfully (`./scripts/build_release.sh`).
