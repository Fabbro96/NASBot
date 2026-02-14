# Changelog

All notable changes to this project are documented in this file.

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
