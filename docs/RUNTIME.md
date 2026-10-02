# 📦 NASBot Minimal Runtime Deployment

This guide covers deploying NASBot as a **minimal, standalone runtime bundle** without needing the Go toolchain, git repository, or source code on the target NAS.

---

## 1. Building the Runtime Bundle

From the development machine with Go installed, run:

```bash
# For ARM64 NAS (Raspberry Pi 4/5, modern ARM NAS)
./scripts/package_runtime.sh --arch arm64

# For AMD64 / x86_64 NAS
./scripts/package_runtime.sh --arch amd64

# For native host architecture
./scripts/package_runtime.sh
```

This compiles the static binary and packages only the production essentials into `dist/runtime/`.

> [!WARNING]
> **Argument order decides which architecture wins.**
> `--config FILE` reloads the whole configuration hierarchy *at the moment it is
> parsed*, so anything a previous flag set is overwritten. Verified:
>
> ```bash
> ./scripts/package_runtime.sh --arch arm64 --config cfg.conf   # cfg.conf: BUILD_ARCH="amd64"
> #   -> bundle contains nasbot-amd64    (config file beat the flag)
> ./scripts/package_runtime.sh --config cfg.conf --arch arm64
> #   -> bundle contains nasbot-arm64    (flag won, it came after)
> ```
>
> Pass `--config` **before** the flags you want to keep, or don't pass it at all.
> The details are in [MODULAR_CONFIG_GUIDE.md](../MODULAR_CONFIG_GUIDE.md).

### Bundle Structure
A real bundle built with `--arch arm64` on an x86_64 host contains **7 files**:

```text
dist/runtime/
├── nasbot                  # Compiled binary (always the canonical name)
├── nasbot-arm64            # Same binary, arch-suffixed copy
├── start_bot.sh            # Production process runner & supervisor
├── common.sh               # Sourced by start_bot.sh (REQUIRED: status/logs break without it)
├── config.example.json     # Configuration template
├── nasbot.config.template  # Optional deployment environment overrides
└── README_RUNTIME.md       # Quick-reference guide
```

Two rules about the binary names:

- `nasbot-arm64` (or `-amd64`, `-armv7`, `-386`) is added **only when the target
  architecture is not the host's own**. A `--arch native` bundle has 6 files and no
  suffixed copy. Verified with real bundles on an x86_64 host:
  `--arch arm64` → 7 files, default → 6 files.
- Both names hold the **same** binary. The suffixed copy exists so
  `start_bot.sh` can tell a pending update for this host from one built for the
  other architecture; on the NAS, use the canonical `nasbot`.

---

## 2. Deploying to your NAS

### Option A: Direct copy (SCP / SFTP / Rsync)
```bash
rsync -avz dist/runtime/ user@your-nas:/opt/nasbot/
```

### Option B: Automated deployment script
```bash
./scripts/deploy_runtime_rsync.sh --target user@your-nas:/opt/nasbot
```

`--target` is **required and has no positional form**. `./scripts/deploy_runtime_rsync.sh user@nas:/opt/nasbot/`
exits with `[ERROR] Unknown option: user@nas:/opt/nasbot/`; running it with no target at
all exits with `[ERROR] Error: --target is required`.

The target path is validated before anything is built or copied, and it must:

- be absolute (`user@host:/path` or `/local/path`);
- not end with `/`;
- have at least two path segments (`/Volume1` and `/mnt` are refused, use `/Volume1/nasbot`);
- not be `/`, `/home`, `/usr`, `/etc`, `/var`, `/tmp`, `/root`, `/opt`, `/srv` or `/boot`.

`--delete` also asks for confirmation on an absolute path, and refuses to run without a
terminal unless you pass `--yes`. Preview first: `--dry-run` needs no confirmation and
changes nothing.

---

## 3. Initial Setup on NAS

SSH into your NAS and navigate to the deployment folder:

```bash
cd /opt/nasbot

# 1. Create and edit your config
cp config.example.json config.json
chmod 600 config.json       # required: see the note below
nano config.json   # set bot_token and allowed_user_id

# 1b. Or let the runner create it with the right mode
./start_bot.sh config init

# 2. Make scripts executable
chmod +x start_bot.sh nasbot

# 3. Install crontab watchdog & start the bot
./start_bot.sh install
```

> [!IMPORTANT]
> **`chmod 600 config.json` is not optional.**
> `cp` honours the shell `umask`, so with the usual `022` the copy lands `0644` and
> every account on the NAS can read `bot_token`. NASBot **refuses to start** when the
> config is readable by group or others (`permissions 0644 expose bot_token to other
> users, want 0600`), so an install without `chmod` does not boot at all.
> `./start_bot.sh config init` creates the file with `0600` for you.

> [!WARNING]
> **Do not use `systemd` to supervise NASBot.**
> NASBot manages its own process lifecycle and automatic self-updates via `start_bot.sh`. Supervised systemd units conflict with in-place binary upgrades, causing dual-instance races.

---

## 4. Managing the Bot

These are the subcommands of the **bundle's** `start_bot.sh`
(`scripts/start_bot_runtime.sh`, shipped as `start_bot.sh`). They differ from
`scripts/start_bot.sh` in the repository — see the table below.

| Command | Action |
|:--------|:-------|
| `./start_bot.sh start` | Start bot in background |
| `./start_bot.sh stop` | Gracefully stop the bot |
| `./start_bot.sh restart` | Restart bot (detects update binaries) |
| `./start_bot.sh status` | Check process status and PID |
| `./start_bot.sh logs [n]` | View the last `n` application log lines (default 50) |
| `./start_bot.sh watch` | Stream logs live (`tail -f`) |
| `./start_bot.sh watchdog` | Restart the bot if it is not running (this is what the cron entry calls) |
| `./start_bot.sh config` | Print the resolved configuration of the runner |
| `./start_bot.sh config init` | Create `config.json` from `config.example.json` with mode `0600` |
| `./start_bot.sh install` | Install the cron entry (`*/5 * * * * start_bot.sh watchdog`) and start the bot |

With no argument the bundle's runner defaults to `status`; an unknown argument prints
the usage and exits `1`.

`scripts/start_bot.sh` (the repository-side runner, used with a source checkout)
implements a smaller set — `start`, `stop`, `restart`, `status`, `watchdog`, `logs`,
`install` — and has **no** `watch` and **no** `config`. It also puts the binary in
`bin/` and the runtime files in `var/`.

Environment overrides honoured by the bundle's runner (pass them inline, they are read
from the environment, not from `nasbot.config.local`):

```bash
NASBOT_LOG_FILE=/var/log/nasbot/app.log NASBOT_PID_FILE=/run/nasbot.pid ./start_bot.sh start
NASBOT_LOG_MAX_SIZE_MB=50 NASBOT_VERBOSE=true ./start_bot.sh status
```

---

## 5. Updates

### Automatic Updates (Recommended)
`update.auto_apply` in `config.json` decides whether a found release is downloaded and
executed without asking. **The two shipped defaults disagree, so set it explicitly:**

| Source | Value |
|:-------|:------|
| Code default, when the `update` section is absent (`internal/app/config_defaults.go`) | `true` — releases are applied unattended |
| `config.example.json` as shipped (`"update": {"auto_apply": false}`) | `false` — you are notified and apply with `/update` |

The example is the safer of the two and is what a copied config gives you, but it is
worth writing the key down rather than relying on either. Two more constraints:

- The check interval is `update.check_interval_hours` (default 1 hour).
- `update.auto_apply` is one of the keys `/configset` may **never** write
  (`bot_token`, `allowed_user_id`, `update.auto_apply` are locked), because applying a
  release without confirmation is remote code execution.

### Manual Update
To apply a binary update manually:
1. Drop the new binary into the folder as `nasbot-update` (or `nasbot-update-arm64` / `nasbot-update-amd64`).
2. Run:
   ```bash
   ./start_bot.sh restart
   ```
3. The script detects `nasbot-update`, backs up the old binary, replaces it, and restarts.

An arch-suffixed candidate is only accepted if it matches the host architecture, so a
stale `nasbot-update-amd64` cannot replace the bot on an ARM64 NAS.

---

## 6. Runtime-Generated Files

Once running, NASBot automatically creates and manages, **next to `start_bot.sh`**:
- `nasbot.log`: Application logging, rotated to `nasbot.log.old` past 10 MB
- `nasbot.pid`: Active process ID (also used as an exclusive `flock` lock: a second
  instance refuses to start)
- `nasbot_state.json`: State cache (statistics, trends, quiet hours settings)

Paths are fixed to the bundle directory unless the `NASBOT_LOG_FILE`,
`NASBOT_PID_FILE`, `NASBOT_STATE_FILE` environment variables are set.

---

## 7. Troubleshooting

- **Bot doesn't start:**
  - Verify `config.json` is valid JSON and contains valid `bot_token` and `allowed_user_id`.
  - Verify the mode: `chmod 600 config.json`. The bot refuses a config readable by
    group or others, and says so with `permissions 0644 expose bot_token to other users`.
  - Check `start_bot.sh logs` or `cat nasbot.log`.
- **Permission errors:**
  - Run `chmod +x start_bot.sh nasbot`.
  - If `status` prints `format_bytes: command not found`, `common.sh` is missing from
    the bundle: copy it from the repository, it is a hard dependency of `start_bot.sh`.
- **Process already running:**
  - Run `./start_bot.sh stop` or inspect with `./start_bot.sh status`.
- **Deployment refuses the target:**
  - See the target validation rules in section 2.
