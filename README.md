# 🖥️ NASBot

> A lightweight, self-hosted Telegram bot to monitor, manage, and protect your Linux NAS/Home Server.

![Platform](https://img.shields.io/badge/Platform-Linux%20ARM64%20%7C%20AMD64-orange)
![License](https://img.shields.io/badge/License-MIT-green)
[![CI](https://github.com/Fabbro96/NASBot/actions/workflows/ci.yml/badge.svg)](https://github.com/Fabbro96/NASBot/actions/workflows/ci.yml)
[![Security](https://github.com/Fabbro96/NASBot/actions/workflows/security.yml/badge.svg)](https://github.com/Fabbro96/NASBot/actions/workflows/security.yml)
[![Release](https://github.com/Fabbro96/NASBot/actions/workflows/release.yml/badge.svg)](https://github.com/Fabbro96/NASBot/actions/workflows/release.yml)
![Provenance](https://img.shields.io/badge/Release%20Provenance-Attested-blue)

A single Go binary that provides a **live server dashboard**, proactive hardware alerts, and Docker management directly inside Telegram—no web UI or heavy daemon required.

---

## ✨ Key Features

- **📊 Live Dashboard**: Real-time CPU, RAM, Swap, Storage (SSD & secondary disks), Network throughput, and Temperatures.
- **🗄️ Disk & Mount Watchdog**: Detects newly attached/removed disks, device node swaps (e.g. `sda1` ➔ `sdb1`), Docker ghost-mount issues with running containers (Plex, Bazarr, etc.), and I/O access errors.
- **💾 Filesystem Space Watchdog**: Periodic free-space scan of the watched mount points (default every 30 min, warn at 85%, critical at 90%, deep scan of `/`). It runs **inside the bot process** — there is no second `fswatchdog` daemon to install or supervise — and its alerts arrive on the same Telegram session as everything else. Configure it under `fs_watchdog` in `config.json`.
- **🐳 Docker Management**: Start, stop, restart, and kill containers with interactive inline buttons.
- **⚙️ Process Manager**: Interactive `/processes` view with CPU/RAM ranking and signal dispatching (SIGTERM/SIGKILL).
- **🤖 AI Diagnostics (Gemini)**: Optional automated analysis of critical alerts and system logs with Google Gemini.
- **🛡️ Proactive Watchdogs**: Continuous monitoring for Network dropouts, Kernel OOMs/hung tasks, RAID degradation, and Docker daemon stalls with automated self-healing.
- **🔄 Auto-Updates**: In-place background upgrades from GitHub Releases with instant Telegram restart notifications.
- **🌍 Multi-Language**: Full native localization for English, Italian, Spanish, German, Chinese, and Ukrainian.
- **📨 Scheduled Reports & Healthchecks**: Morning/evening summaries and built-in [Healthchecks.io](https://healthchecks.io) integration.

---

## 🚀 Quick Start

### Option 1: Docker Compose

> The native runtime bundle (Option 2) is the supported deployment path. Compose is
> documented because it exists, not because it is safer: it needs `privileged: true`,
> `pid: host`, `network_mode: host` and the Docker socket, which together are root on
> the NAS. Read [docs/SECURITY.md](docs/SECURITY.md#attack-surface) before deploying it.

1. Clone and navigate to the project:
   ```bash
   git clone https://github.com/Fabbro96/NASBot.git
   cd NASBot
   ```
2. Create your configuration:
   ```bash
   cp config.example.json config.json
   chmod 600 config.json          # required: the file holds bot_token
   # Edit config.json and set bot_token and allowed_user_id
   ```
   > [!IMPORTANT]
   > `chmod 600` is not optional. With the default `umask 022`, `cp` leaves the
   > file `0644`, so every account on the NAS can read your bot token. NASBot
   > **refuses to start** when `config.json` is readable by group or others.
3. Start the container:
   ```bash
   docker compose up -d
   ```

---

### Option 2: Minimal Standalone Runtime (No Source / No Go Toolchain)

For clean NAS setups where you only want the executable binary and runner script:

👉 **[Read the Runtime Deployment Guide (docs/RUNTIME.md)](docs/RUNTIME.md)**

```bash
# Build the bundle on your workstation
./scripts/package_runtime.sh --arch arm64

# On the NAS, after copying dist/runtime into place
cp config.example.json config.json
chmod 600 config.json            # or: ./start_bot.sh config init
./start_bot.sh install
```

---

### Option 3: Build from Source

```bash
cp config.example.json config.json
chmod 600 config.json            # required: the file holds bot_token
# Edit config.json with your credentials
go build -o nasbot .             # single package main: "./..." fails
./nasbot
```

---

## ⚙️ Configuration

Only two fields in `config.json` are required to get started:

```json
{
  "bot_token": "YOUR_TELEGRAM_BOT_TOKEN",
  "allowed_user_id": 123456789,
  "gemini_api_key": "",
  "timezone": "Europe/Rome",
  "paths": {
    "ssd": "/"
  }
}
```

> [!TIP]
> **Modular Deployments:** NASBot supports environment variable overrides and configuration inheritance. Check out [MODULAR_CONFIG_GUIDE.md](MODULAR_CONFIG_GUIDE.md) and [nasbot.config.template](nasbot.config.template) for advanced setups.

---

## 🎮 Commands Reference

At startup NASBot pushes its main command list to Telegram itself (`setMyCommands`), so those commands show up in the chat menu with no manual step. The commands marked ⚙️ are **not** in that pushed list: type them, or add them once through @BotFather using the ready-made list in [`docs/operations/BOTFATHER_COMMANDS.txt`](docs/operations/BOTFATHER_COMMANDS.txt).

### 📊 System & Monitoring
| Command | Description |
|:--------|:------------|
| `/status`, `/start` | Live system overview (CPU, RAM, Disks, Docker, Uptime) |
| `/quick`, `/q` | Compact diagnostic snapshot |
| `/top`, `/processes` ⚙️ | Top processes; `/processes` opens the interactive manager with signal buttons |
| `/sysinfo` | Detailed OS, kernel, CPU model, and hardware specifications |
| `/temp` | Hardware temperatures (CPU cores, NVMe/SATA drives) |
| `/diskpred` | Linear regression disk space exhaustion prediction |
| `/diskinfo` | Disk usage on demand, with the last check and last deep scan times |
| `/net`, `/speedtest` | Network interface statistics and on-demand speedtest |
| `/ping` ⚙️ | Check that the bot process is alive |

### 🐳 Docker & Power
| Command | Description |
|:--------|:------------|
| `/docker` | Interactive container management dashboard |
| `/dstats` | Real-time container resource usage metrics |
| `/container <name>` ⚙️ | Actions on a single container |
| `/kill <name>` ⚙️ | Force kill a container |
| `/restartdocker` | Restart the system Docker daemon |
| `/adblock` ⚙️ | Pi-hole/AdGuard control panel: pause and resume filtering |
| `/reboot`, `/shutdown` | NAS power management (with confirmation) |
| `/forcereboot` ⚙️ | Immediate forced restart without confirmation |

### 🤖 AI, Logs & Utilities
| Command | Description |
|:--------|:------------|
| `/ask <query>` ⚙️ | Ask Gemini AI about recent log events and system behavior |
| `/agy <args>` ⚙️ | Run the Antigravity CLI (`agy`) and return its output |
| `/cmd <command>` ⚙️ | Run a program on the host — **disabled by default** and limited to the binaries in `shell_command.allowed_binaries`, see [docs/SECURITY.md](docs/SECURITY.md) |
| `/report` | Generate a full diagnostic and health report |
| `/logs`, `/logsearch` | View recent system logs or search by pattern |
| `/config`, `/settings` | Inspect settings, change language, or toggle quiet hours |
| `/configjson` ⚙️ | Show the full `config.json` with secrets redacted |
| `/configset <json>` ⚙️ | Apply a JSON patch to `config.json`; credentials are locked |
| `/health` | Healthchecks.io ping status and watchdog history |
| `/backup [user_id]` ⚙️ | Send a timestamped backup of the configuration as a zip |
| `/version` ⚙️ | Show the running bot version |
| `/changelog` | Show the release notes of the running version |
| `/update` | Check for and apply the latest GitHub release |
| `/help` | List every available command |

> [!NOTE]
> **Aliases:** `/start` → `/status`, `/q` → `/quick`, `/processes` → `/top`, `/prediction` → `/diskpred`, `/healthchecks` → `/health`, `/v` → `/version`, `/shell` and `/exec` → `/cmd`.
>
> Wake-on-LAN (`/wol`) was **removed** in `v0.12.1` and is not part of the bot. See [docs/CHANGELOG.md](docs/CHANGELOG.md).

---

## 🔄 Auto-Updates

NASBot features a built-in updater that periodically queries GitHub Releases:
- Automatically downloads compatible binaries for your architecture (`arm64`, `amd64`).
- Verifies checksums, replaces the active binary, and reboots cleanly.
- Sends a confirmation message on Telegram with the version diff upon restart.

---

## 🛡️ Security & Hardening

- **Access Control:** The bot strictly restricts execution to the Telegram ID configured in `allowed_user_id`. All unauthorized messages are dropped.
- **Config File Permissions:** `config.json` must be mode `0600`. NASBot refuses to boot when the file is readable by group or others, so `chmod 600 config.json` belongs in every install procedure.
- **Secret Isolation:** `config.json` is git-ignored and sanitized in logs and configuration previews.
- **Git Hooks:** `./scripts/setup_hooks.sh` writes the `pre-commit`, `pre-push` and `commit-msg` hooks **and activates them** by setting `git config core.hooksPath .githooks` for this clone. Run it once after cloning, otherwise no hook ever fires.
- Read [docs/SECURITY.md](docs/SECURITY.md) for vulnerability reporting, the container attack surface, and leak response protocols.

---

## 🧪 Testing & CI/CD

```bash
# Run all tests with race detector
go test -race ./...

# Run the complete CI quality gate locally (what the pre-push hook runs)
./scripts/ci_guard.sh

# Shell lint: a SEPARATE CI job, not part of ci_guard.sh
./scripts/shellcheck_all.sh
```

- **CI Pipeline:** Automated formatting, static analysis (`go vet`), race detection, and GitHub release generation with attested build provenance.
- **Release gate:** `scripts/ci_guard.sh release vX.Y.Z` additionally requires a valid semver tag **and** a `## vX.Y.Z - YYYY-MM-DD` section in [docs/CHANGELOG.md](docs/CHANGELOG.md).
- **Full History:** See [docs/CHANGELOG.md](docs/CHANGELOG.md) for release notes.
- **Contributing:** [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) · **Governance:** [docs/governance/BRANCH_PROTECTION.md](docs/governance/BRANCH_PROTECTION.md)
