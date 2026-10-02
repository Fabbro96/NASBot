# Security & Hardening Policy

## Scope
This policy applies to local development, pull requests, and releases.

## Secret Management
- Never commit real secrets (Telegram token, Gemini API key, credentials).
- Use `config.example.json` as the only committed template.
- Keep real values only in local `config.json` (already gitignored, and never tracked:
  `scripts/quality_check.sh` fails if `config.json` or `config.json.bak` shows up in
  `git ls-files`).
- If a secret leaks, rotate it immediately and invalidate old tokens/keys.

### The config file must be mode 0600

`config.json` holds `bot_token`, which is the bot's only access control: anyone holding
it can talk to the bot as the owner.

```bash
cp config.example.json config.json
chmod 600 config.json
```

`cp` honours the shell `umask`, so with the usual `022` the copy lands `0644` and every
account on the NAS can read the token. NASBot **refuses to boot** on such a file
(`permissions 0644 expose bot_token to other users, want 0600`) and tightens an existing
file to `0600` with a warning in the log. `./start_bot.sh config init` creates the file
with the correct mode. This is not optional polish: an install without `chmod` does not
start.

Backups (`/backup`) are written with `0600` for the same reason, and
`nasbot.log`/`nasbot.pid` are opened `0600` by the bot itself.

## Commit Guardrails
- `scripts/secret_scan.sh` has two modes: `--staged` (the default) scans the **git
  index**, i.e. what is about to be committed; `--repo` scans every tracked file in the
  working tree.
- Two tiers of patterns:
  - Tier 1, high-confidence credential shapes (Telegram bot tokens, `AIza…` Google keys,
    GitHub classic and fine-grained PATs, AWS access key ids, Slack tokens, PEM private
    keys) is scanned with **no exclusions at all** — tests, README, docs and examples
    included. A real token of one of those shapes is never legitimate in a tracked file.
  - Tier 2, credential-shaped config keys (`bot_token`, `gemini_api_key`) skips
    `*_test.go` files, where a literal value is the point of the test, and ignores
    documented placeholders (`YOUR_…`, `CHANGEME`, `EXAMPLE`, `<ANGLE_BRACKETS>`, …)
    everywhere.
- `scripts/setup_hooks.sh` writes the hooks **and activates them**
  (`git config core.hooksPath .githooks`). Without that last step git never runs them.
  In a fresh clone they are inert by default, so run the script once.
  - `pre-commit`: `gofmt -l`, `scripts/secret_scan.sh --repo`, `scripts/quality_check.sh`
  - `pre-push`: the full `scripts/ci_guard.sh`
  - `commit-msg`: Conventional Commits, `^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9_-]+\))?: .+`,
    merge commits exempt
- CI adds Gitleaks (`Secret Scan` job), so a hook bypass does not reach `main`.

## Branch & Release Rules
- Before merge/release, run:
  - `scripts/ci_guard.sh`
  - `scripts/shellcheck_all.sh`
  - `./scripts/build_release.sh`
- Create annotated tags only from validated commits.
- `scripts/ci_guard.sh release <tag>` adds two more checks to the above: a valid semver
  tag **and** a `## <tag>` section in `docs/CHANGELOG.md`. The release workflow runs it
  before building or publishing anything.
- What the local gate actually runs: `secret_scan.sh --repo`, `quality_check.sh`,
  "config.json is not tracked", `gofmt -l`, `go vet ./...`,
  `go test -tags deadlock -race -count=1 -coverprofile=…` with a 100% statement-coverage
  threshold, `go build ./...`.
- **Shell lint runs in the local gate too.** `ci_guard.sh` executes
  `scripts/shellcheck_all.sh`, the same entrypoint the `ShellCheck` job uses in
  `.github/workflows/ci.yml`, so `pre-push` catches shell problems before the push and
  not only the CI.
- Other required checks: `Secret Scan`, `Build & Test` (Go 1.22.x and 1.23.x),
  `Deadlock & Race (Go 1.23.x)`, `Dependency Review`, `CodeQL Analysis`.
  `Go Vulnerability Check` (`govulncheck`) runs but is not required.
- Branch protection baseline: see [governance/BRANCH_PROTECTION.md](governance/BRANCH_PROTECTION.md).
- GitHub UI rollout guide: see [governance/GITHUB_RULESET_SETUP.md](governance/GITHUB_RULESET_SETUP.md).
- Ruleset automation: run `scripts/apply_github_rulesets.sh`.

## Attack Surface

The bot is a remote root-like control panel for a NAS: it reboots the host, kills
processes, starts and stops containers and reads `/dev`, `/sys` and the Docker API.
Anything that reaches it reaches the machine. Two surfaces deserve to be written down.

### Container deployment (docker-compose.yml)

`docker-compose.yml` is the *secondary* deployment path; the supported one is the native
binary plus `start_bot.sh`. The compose file grants, and needs:

| Setting | Why the bot needs it | What breaks without it |
|:--------|:---------------------|:-----------------------|
| runs as **root** (no `USER` in the image) | reading `/dev`, `/sys`, SMART data and talking to the Docker daemon all require it | disk, RAID and container monitoring break at runtime, not at build time |
| `privileged: true` | read SMART data (`/dev/sdX`), sensors (`/sys/class/hwmon`), `dmidecode` tables, mount info | disk health, RAID state, kernel info, SMART dashboard sections return empty or are refused |
| `pid: host` | read `/proc/<pid>` of host processes for the process monitor and the per-PID load counters | the process monitor sees only the container's own PIDs |
| `network_mode: host` | reach services bound to `127.0.0.1` or the LAN IP | outbound NAT still works, but host-local services become invisible |
| `/var/run/docker.sock` mounted | `/docker`, `/dstats`, `/container`, `/kill`, `/restartdocker`, prune | no container management at all |
| `/dev:/dev:ro`, `/sys:/sys:ro` | temperatures, CPU, RAID state, block devices | no temperature/CPU/RAID readings |
| `/:/hostfs:ro` | measure free space per mount point, resolve host paths | no per-mount disk reporting |

Together these are **equivalent to root on the NAS**. That is a deliberate, documented
trade-off, not an accident, and nothing is weakened for convenience: the container must
be able to read the hardware and talk to the daemon. The mitigations are elsewhere:

- The image ships **no credentials**: `config.json` is bind-mounted from the host at
  `0600`, so no secret is baked into a layer. `.dockerignore` exists for the same reason:
  without it `COPY . .` would put `config.json` and `.git` into the builder stage.
- The host filesystem is mounted read-only; the bot never writes to it.
- `config.json` is never tracked by git (`quality_check.sh` enforces this for
  `config.json` and `config.json.bak`).
- Anyone who can reach the container can reach the host. Treat the bot token and access
  to the Docker socket as equivalent secrets.

If you do not need container management, do not use the compose file: the native bundle
runs as a single unprivileged user process.

### `/cmd` (aliases `/shell`, `/exec`)

`/cmd <command> [args…]` executes a program on the host. Historically it handed the whole
line to `sh -c`, so any authenticated chat had arbitrary shell execution with the
permissions of the NASBot process — under the compose deployment, root.

It is now behind the `shell_command` section of `config.json`, **disabled by default**:

```json
"shell_command": {
  "enabled": false,
  "allowed_binaries": [],
  "timeout_seconds": 30,
  "max_output_chars": 4000
}
```

Three properties, and each one matters:

- **Default denied, and three ways to be denied.** `enabled: false`, an empty
  `allowed_binaries`, and a missing `shell_command` section all mean `/cmd` answers with
  a refusal. A configuration that was truncated or lost in a merge behaves like an
  explicit "off", never like a licence to run.
- **The allowlist is a list of binary *names*, never of shell lines.** An entry is
  resolved through `PATH` and the system directories; everything the user typed after the
  name is passed to the process as `argv`. Nothing is re-parsed by a shell, so
  `/cmd sh -c id` runs nothing unless `sh` is itself in the list.
- **Allowlist entries are validated as bare names.** A path, a traversal or any name
  containing a separator is dropped before use, so no entry can point outside the
  intended set.

`enabled: true` with an empty allowlist runs nothing.

Rules for anything added next:

- A new command that reaches the host is a new capability: it belongs behind an explicit
  opt-in in `config.json`, not behind "only the admin uses it".
- Never let a chat write `bot_token`, `allowed_user_id` or `update.auto_apply`; they are
  refused by `/configset` (`lockedConfigPaths` in `internal/app/config.go`).
- Prefixes, globs and "anything not in the denylist" are not allowlists.

## Incident Response (Leak)
1. Rotate leaked secrets immediately.
2. Remove secret from tracked history if present.
3. Revoke affected bot/API access.
4. Publish a short post-incident note in changelog/release notes.

## Local Setup (Required)
```bash
# Installs and activates all three hooks in one step
./scripts/setup_hooks.sh

# Equivalent manual form (writes the files, then activates them)
git config core.hooksPath .githooks
chmod +x .githooks/pre-commit .githooks/pre-push .githooks/commit-msg scripts/secret_scan.sh
```

## Reporting a Vulnerability

Open a private security advisory on the repository rather than a public issue, and do
not include a live token in the report: describe the shape of the value and the file it
was found in.
