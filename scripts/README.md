# NASBot Scripts

This directory contains all the automation, deployment, and testing scripts used by NASBot.
All scripts should be executed from the root of the repository (e.g., `./scripts/start_bot.sh`).

## 🚀 Running & Deployment

- `start_bot.sh`: The main entry point to install, start, stop, and restart the bot locally. Subcommands: `start`, `stop`, `restart`, `status`, `watchdog`, `logs [n]`, `install`, plus the global `--dry-run` / `--yes`. It puts the binary in `bin/` and the runtime files in `var/`.
- `start_bot_runtime.sh`: The runner shipped **inside** the runtime bundle (it is copied to `dist/runtime/` as `start_bot.sh`). Same subcommands plus `watch` and `config [init]`, which the source-side runner does not have. It sources `common.sh` with `NASBOT_NO_AUTO_LOAD=1` and therefore reads its settings from `NASBOT_*` environment variables only. Do not run it from the repository root: it expects to sit next to `nasbot`.
- `package_runtime.sh`: Creates a minimal, standalone `dist/runtime` folder to deploy on your NAS without the Go toolchain. Cross-architecture builds add a suffixed copy of the binary (`nasbot-arm64`), a native build does not.
- `deploy_runtime_rsync.sh`: Validates the target, builds the bundle and pushes it with `rsync`. **`--target` is required and has no positional form.** Adds `--delete`, which needs a terminal or `--yes`.
- `build_release.sh`: Used by CI to build the ARM64 and AMD64 binaries for GitHub Releases.
- `common.sh`: Contains shared functions, styling, and configuration parsers used by all deployment scripts. Sourcing it auto-calls `load_config`; set `NASBOT_NO_AUTO_LOAD=1` to suppress that.

## 🧪 CI/CD & Testing

- `setup_hooks.sh`: **Run this once after cloning!** It writes the local Git hooks (`pre-commit`, `pre-push`, `commit-msg`) **and activates them** by setting `git config core.hooksPath .githooks` for this clone. Without that last step git never runs the files, so no guard applies locally.
- `ci_guard.sh`: The main CI script. Modes: `ci` (default) and `release <tag>`. It runs `secret_scan.sh --repo`, `quality_check.sh`, asserts that `config.json` is not tracked, `gofmt -l`, `go vet ./...`, the unit tests with the race and deadlock detectors, a **100% statement-coverage** threshold (`NASBOT_MIN_COVERAGE` is read for measurement; release mode never relaxes it), and `go build ./...`. In `release` mode it additionally requires **both** a valid semver tag **and** the matching `## <tag>` section in `docs/CHANGELOG.md`. Used by GitHub Actions and by the local `pre-push` hook.
- `quality_check.sh`: Ensures repository structure health (required documentation present, legacy paths blocked, no runtime artifact tracked). It is run by `ci_guard.sh` and by the `pre-commit` hook.
- `secret_scan.sh`: Regex scanner for accidental commits of API keys and Telegram tokens, in two tiers (high-confidence shapes everywhere; credential-shaped config keys skip `*_test.go` and ignore placeholders). `--staged` scans the git index (default), `--repo` scans all tracked files.
- `shellcheck_all.sh`: Runs the `shellcheck` linter against all Bash scripts in this repository. **Not** part of `ci_guard.sh` and not covered by the local `pre-push` hook: CI runs it as its own `ShellCheck` job, which is in the `main` ruleset's required checks, so it blocks the merge after the push. Run it locally yourself.
- `apply_github_rulesets.sh`: A utility to enforce branch protection rules on GitHub using the `gh` CLI.

## 🔧 Environment Configuration

Scripts are designed to be modular. Do **not** edit these scripts directly to change settings! Instead, read `MODULAR_CONFIG_GUIDE.md` in the repository root and use a `nasbot.config.local` file to override variables.

Two things to know before you rely on a config file:

- It is only consulted when you pass `--config FILE`, and `--config` re-runs the whole hierarchy while it is parsed, so it overrides any flag given **before** it. Put `--config` first.
- Environment variables (`NASBOT_*`) are the reliable override: they are honoured whether or not a config file is loaded. The list of variables each script actually reads is in `MODULAR_CONFIG_GUIDE.md`.
