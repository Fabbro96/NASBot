# Changelog

All notable changes to this project are documented in this file.

Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/): one `## vX.Y.Z - YYYY-MM-DD`
section per tag, in reverse chronological order. The date is the tag's creation date
(`git for-each-ref --sort=creatordate refs/tags`), not the date of the last commit.

> **Tags without a section:** `v0.2.0` through `v1.4.0` predate the rule above and have no
> section here. Their release notes live on the GitHub Releases page. `scripts/ci_guard.sh
> release <tag>` only requires a section for the tag being released, so those releases are
> unaffected — but new tags must follow this format.

## v1.7.0 - 2026-10-07

### Security
- **Config file permissions are now enforced.** `config.json` must be mode `0600`:
  loading fails with `permissions 0644 expose bot_token to other users, want 0600`
  instead of starting with a world-readable token, and an existing file is tightened
  with a warning in the log. Every install procedure in the documentation
  (`README.md`, `docs/RUNTIME.md`, `docs/CONTRIBUTING.md`) now says `chmod 600`, and
  `./start_bot.sh config init` creates the file with the right mode.
- **Centralized secret redaction** in `pkg/model/secrets.go`. Transport errors embed the
  full request URL (`.../bot<TOKEN>/getMe`), so every logged Telegram error used to carry
  the bot token; one sanitizer now masks credentials wherever errors are formatted,
  instead of one copy per package.
- **`/configset` can no longer write credentials.** `bot_token`, `allowed_user_id` and
  `update.auto_apply` are refused (and reported back as refused), so a stolen chat
  cannot turn itself into full control or into unattended remote code execution.
- **`scripts/secret_scan.sh` rewritten around two tiers.** High-confidence credential
  shapes are scanned with no exclusions, anywhere; credential-shaped config keys skip
  `*_test.go` and ignore documented placeholders. `--staged` (the default) scans the git
  index, `--repo` scans tracked files.
- **`scripts/setup_hooks.sh` activates the hooks it writes** with
  `git config core.hooksPath .githooks`. Before this, the hook files were written and
  never run.

### Changed
- **`pkg/config` deleted.** It was a fork of the configuration code that nothing imported,
  with its own diverging copy of the sanitization.
- **The filesystem watchdog runs inside the bot.** It is a lane of the watchdog manager
  (`internal/app/monitors_manager.go`), not a second binary built with the `fswatchdog`
  build tag — that entry point, which no pipeline ever compiled, is gone. Its alerts
  arrive on the normal Telegram session.
- **`scripts/ci_guard.sh` treats a malformed `NASBOT_MIN_COVERAGE` as a failure**, never
  as a disabled gate, and computes per-package statement coverage from the profile.
- **Documentation rewritten against the code.** Removed the `/wol` command (removed from
  the bot in `v0.12.1`), documented `--target` instead of the positional argument that
  never worked, listed the real bundle contents, documented the two conflicting
  `update.auto_apply` defaults, added `chmod 600` everywhere, documented the container
  attack surface, and rewrote `MODULAR_CONFIG_GUIDE.md` against the keys the scripts
  actually read (35 of the 45 keys in `nasbot.config.template` were dead and are gone)
  plus the real `--config` precedence.

### Added
- **`/cmd` is off by default and allowlisted.** It used to run `sh -c` with whatever the
  authorized user typed, which turns a leaked bot token into a root shell on the NAS: the
  bot runs as root inside a privileged container with the Docker socket mounted. It now
  runs only the binaries named in `shell_command.allowed_binaries`, never through a shell,
  and answers with a refusal unless the section is enabled in `config.json`
  (`shell_command.enabled`, plus the list). `/configset` cannot turn it on from a chat.
  **To keep the old behaviour**, set `"shell_command": {"enabled": true,
  "allowed_binaries": ["ls", "df", ...]}` and restart.
- **The weekly Docker prune asks first.** `docker system prune -a -f` is irreversible —
  an image no container refers to cannot be pulled again without network access, so a
  rollback image is lost — and it was the only destructive action without confirmation,
  while `container kill` and `reboot` both had one. The prune now shows an inventory
  (`docker system df -v`: unused images, stopped containers, dangling networks), the
  count and names per category, the irreversibility warning, and only runs after an
  explicit confirmation. It **refuses to run at all** when the inventory cannot be
  produced. Confirmation is single-use and does not survive a restart, so a stale keyboard
  cannot execute anything.
- **`/diskinfo`** for on-demand disk usage with the last check and last deep scan times.

### Fixed
- **Half of the inline keyboard buttons did nothing.** Callback routing matched prefixes by
  ranging over a Go map, so the order was random, and a catch-all prefix of `""` matched
  everything: about half the time it won and the button did nothing, silently, for the
  health, AdBlock, container and process callbacks. Longest-prefix matching now runs
  first, and the catch-all only when nothing else matches.
- **A configuration reload could kill the process.** `loadConfig` decoded `config.json`
  into the live config struct while half a dozen goroutines read it. `encoding/json` reuses
  a non-nil map and writes into it, so a `/config` while the bot was running could trigger
  `fatal error: concurrent map iteration and map write`, which no `recover()` can catch.
  The published config is now an immutable snapshot swapped atomically.
- **A failed report send looped.** The scheduler did not record the attempt, so a report
  that Telegram rejected for length restarted immediately and called Gemini in a tight
  loop. Bounded retries with backoff, and the report is split to the message limit.
- **The updater installed unverified binaries.** A downloaded release asset was renamed
  over the running executable with no integrity check, and a truncated download was
  installed as if it had succeeded, because `io.Copy` with a `LimitReader` reports no
  error at the limit. The release now verifies the asset against `SHA256SUMS.txt` before
  installing, refuses an oversized or short download, and propagates a failed rename
  instead of exiting as if it had succeeded.
- **The Gemini API key was sent in the URL.** Transport errors report the full URL, and
  those errors reached both the log and Telegram, so the key was written to
  `var/nasbot.log` and sent in chat. It now travels in a header, and errors are sanitized.
- **`/cmd` children survived.** The runner killed only `sh`, so `sh -c 'sleep 300 &'` left
  an orphan behind. It now kills the whole process group.
- **The forced reboot could loop.** The healthcheck watchdog rebooted unconditionally after
  six minutes of an unreachable pinger, with no configuration and no quiet hours. It is now
  configurable (`healthchecks.force_reboot_on_prolonged_down`, `force_reboot_after_minutes`,
  default 15 minutes), announces itself even during quiet hours, and fires **at most once
  per downtime episode** with a minimum interval, so a network outage cannot reboot the NAS
  every fifteen minutes.
- **RAID and hung-task detection could not fire.** The RAID parser skipped the second line
  of `/proc/mdstat`, the one that carries `[U_]` and `(F)`, so a degraded or failed array
  was silent. The hung-task keyword was a regex used with `strings.Contains`.
- **Alerts were lost.** Cooldowns advanced before the quiet-hours check, so an alert
  suppressed at night was never delivered; disk alerts ignored the cooldown entirely.
- **Telegram edits could fail twice.** `editMessage` did not respect the 4096 character
  limit and its fallback retried with the same markup, so a long message failed and then
  failed again.
- **`build_release.sh` leaked the build machine.** Binaries carried 61 absolute paths,
  including the build user's home directory: different hashes on different machines, and
  the published artifact disclosed who built it. Fixed with `-trimpath` and `-s -w`.
- **Both GitHub rulesets were unsatisfiable.** `main` required a check named
  `CodeQL Analysis (go)` that no workflow produces, so no pull request could ever be
  merged, and required signed commits while none are signed. A tag ruleset blocked the
  creation of `v*` tags for everyone, which would have made every release impossible.
- **The release pipeline had not run since 2026-09-01.** `permissions:` written inside a
  step is not valid: GitHub rejects the whole file, creates no job, and reports a failed
  run on every push with `This run likely failed because of a workflow file issue`. YAML
  parsers accept it happily, so every other check stayed green while the pipeline was
  dead. The single job is now four, each carrying its own job-level permissions — the same
  least-privilege result, at the granularity GitHub supports. The tag check also runs
  before the checkout, so a bad tag is refused with an explicit message instead of dying
  in the ref resolver. `scripts/check_workflows.sh` is wired into the gate so a file GitHub
  would refuse cannot pass silently again: unparseable YAML, duplicate keys and permissions
  on a step, each tested in both directions.

### Changed
- **`update.auto_apply` defaults to `false`** in both the code and `config.example.json`,
  which disagreed. A bot that replaces its own binary over the network is the largest
  supply-chain surface here, and it is now opt-in.
- **`scripts/ci_guard.sh` gained a coverage gate** (`NASBOT_MIN_COVERAGE`, default 36)
  and runs `shellcheck_all.sh` in the local gate, not only in CI.

### Validation
- Quality gate passing (`scripts/ci_guard.sh`).
- Tests passing under the race detector with the deadlock tag.
- Statement coverage **36.2%** (`nasbot` and `internal/cmdexec` 100%, `pkg/model` 99.1%,
  `internal/format` 97.5%, `pkg/commands` 53.2%, `internal/app` 28.4%). The gate holds
  this floor so it cannot slip; the goal is to raise it, package by package.
- Documentation gate passing (`scripts/quality_check.sh`).
- Not verified here: a real NAS, real Telegram delivery, and ARM64 hardware.

## v1.6.2 - 2026-09-01

### Added
- **100% translation coverage.** Every key present in the English map exists in all 6
  languages (`it`, `en`, `es`, `de`, `zh`, `uk`), pinned by
  `translations_coverage_test.go` and `translations_deep_test.go`.
- **Hardened Telegram Markdown fallback:** `safeSend` retries with plain text when the
  API rejects a parse-mode message, instead of dropping the notification.

### Changed
- Documentation streamlined and the runtime deployment guide consolidated into
  `docs/RUNTIME.md`.

## v1.6.1 - 2026-08-24

### Changed
- README rewritten as a compact overview, with the deployment instructions moved to
  `docs/RUNTIME.md`.
- `scripts/package_runtime.sh` cleans its output directory before building, so a second
  packaging run with another architecture can no longer leave the previous binary in
  the bundle.

## v1.6.0 - 2026-08-24

### Added
- **Disk & Mount Point Watchdog:** real-time disk mount monitoring
  (`internal/app/monitor_disks.go`) detecting added, removed and unmounted disks, and
  I/O access errors.
- **Docker Ghost Mount Detection:** detects disk reconnections and device node changes
  (e.g. `sda1` -> `sdb1`) and warns when running containers (Plex, Bazarr, FileBrowser)
  still hold stale mount references.
- **Container Impact Inspection:** identifies the containers mapping an affected mount
  point and offers Telegram buttons to restart them.
- **Virtual filesystem types** and exact mount matching in `getContainersUsingMount`,
  plus a configured-path I/O fallback.

### Changed
- **`/status` UI/UX refactoring:** clean section layout, consistent storage formatting,
  safe Markdown escaping, no stray newlines.
- **Translations:** new disk-monitoring keys in all 6 languages.

## v1.5.0 - 2026-08-14

### Added
- **Report scheduling by weekdays** and a deep configuration merge, replacing the
  per-report period field.
- **Docker Readiness:** provided `Dockerfile` (Alpine-based) and `docker-compose.yml` for
  containerized deployments.
- **Testing:** comprehensive test coverage with 30+ new unit and integration tests across
  application logic, commands, configurations, and utilities.
- **S.M.A.R.T. Monitoring:** HDD SMART monitoring refactored to use a thread-safe
  `SmartCache` in `MonitorState`, optimizing system calls.
- **CI/CD:** `govulncheck` and `deadlock-race-gate.yml` in the pipeline.
- **Configuration:** structure updated to support a robust `SecondaryDisks` mapping.

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
- Release workflow validates the semantic tag before publishing assets. The changelog
  presence check added around this release was **removed again in `v0.2.1`** and is
  enforced once more today: `scripts/ci_guard.sh release <tag>` requires both a valid
  semver tag and a matching `## <tag>` section in `docs/CHANGELOG.md`.
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
