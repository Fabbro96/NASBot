# NASBot Scripts

This directory contains all the automation, deployment, and testing scripts used by NASBot.
All scripts should be executed from the root of the repository (e.g., `./scripts/start_bot.sh`).

## 🚀 Running & Deployment

- `start_bot.sh`: The main entry point to install, start, stop, and restart the bot locally. It sets up watchdogs, log rotation, and manages the binary.
- `start_bot_runtime.sh`: Used specifically in "runtime bundles" (production environments without Go source).
- `package_runtime.sh`: Creates a minimal, standalone `dist/runtime` folder to deploy on your NAS without the Go toolchain.
- `deploy_runtime_rsync.sh`: Helper to automatically push the runtime bundle to a remote NAS via `rsync`.
- `build_release.sh`: Used by CI to build the ARM64 and AMD64 binaries for GitHub Releases.
- `common.sh`: Contains shared functions, styling, and configuration parsers used by all deployment scripts.

## 🧪 CI/CD & Testing

- `setup_hooks.sh`: **Run this once after cloning!** Configures local Git hooks (`pre-commit`, `pre-push`, `commit-msg`) to run formatters, linters, and the full test suite locally so you catch errors before pushing to GitHub.
- `ci_guard.sh`: The main CI script. It runs `gofmt`, `go vet`, unit tests (with race and deadlock detectors), and checks changelog requirements. It is used in GitHub Actions and by the local `pre-push` hook.
- `quality_check.sh`: Ensures repository structure health (checks for required documentation, blocks legacy paths).
- `secret_scan.sh`: A fast regex-based scanner to prevent accidental commits of API keys and Telegram tokens. Used by the local `pre-commit` hook.
- `shellcheck_all.sh`: Runs the `shellcheck` linter against all Bash scripts in this repository.
- `apply_github_rulesets.sh`: A utility to enforce branch protection rules on GitHub using the `gh` CLI.

## 🔧 Environment Configuration

Scripts are designed to be modular. Do **not** edit these scripts directly to change settings! Instead, read `MODULAR_CONFIG_GUIDE.md` in the repository root and use a `nasbot.config` file to override variables.
