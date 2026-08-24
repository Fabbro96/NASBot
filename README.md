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
- **🐳 Docker Management**: Start, stop, restart, and kill containers with interactive inline buttons.
- **⚙️ Process Manager**: Interactive `/processes` view with CPU/RAM ranking and signal dispatching (SIGTERM/SIGKILL).
- **🤖 AI Diagnostics (Gemini)**: Optional automated analysis of critical alerts and system logs with Google Gemini.
- **🛡️ Proactive Watchdogs**: Continuous monitoring for Network dropouts, Kernel OOMs/hung tasks, RAID degradation, and Docker daemon stalls with automated self-healing.
- **🔄 Auto-Updates**: In-place background upgrades from GitHub Releases with instant Telegram restart notifications.
- **🌍 Multi-Language**: Full native localization for English, Italian, Spanish, German, Chinese, and Ukrainian.
- **📨 Scheduled Reports & Healthchecks**: Morning/evening summaries and built-in [Healthchecks.io](https://healthchecks.io) integration.

---

## 🚀 Quick Start

### Option 1: Docker Compose (Recommended)

1. Clone and navigate to the project:
   ```bash
   git clone https://github.com/Fabbro96/NASBot.git
   cd NASBot
   ```
2. Create your configuration:
   ```bash
   cp config.example.json config.json
   # Edit config.json and set bot_token and allowed_user_id
   ```
3. Start the container:
   ```bash
   docker-compose up -d
   ```

---

### Option 2: Minimal Standalone Runtime (No Source / No Go Toolchain)

For clean NAS setups where you only want the executable binary and runner script:

👉 **[Read the Runtime Deployment Guide (docs/RUNTIME.md)](docs/RUNTIME.md)**

```bash
# Build the bundle on your workstation
./scripts/package_runtime.sh --arch arm64

# Copy dist/runtime to your NAS and start
./start_bot.sh install
```

---

### Option 3: Build from Source

```bash
cp config.example.json config.json
# Edit config.json with your credentials
go build -o nasbot .
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

### 📊 System & Monitoring
| Command | Description |
|:--------|:------------|
| `/status`, `/start` | Live system overview (CPU, RAM, Disks, Docker, Uptime) |
| `/quick`, `/q` | Compact diagnostic snapshot |
| `/top`, `/processes` | Top processes with interactive process control buttons |
| `/sysinfo` | Detailed OS, kernel, CPU model, and hardware specifications |
| `/temp` | Hardware temperatures (CPU cores, NVMe/SATA drives) |
| `/diskpred` | Linear regression disk space exhaustion prediction |
| `/net`, `/speedtest` | Network interface statistics and on-demand speedtest |

### 🐳 Docker & Power
| Command | Description |
|:--------|:------------|
| `/docker` | Interactive container management dashboard |
| `/dstats` | Real-time container resource usage metrics |
| `/restartdocker` | Restart the system Docker daemon |
| `/reboot`, `/shutdown` | NAS power management (with confirmation) |
| `/forcereboot` | Immediate forced restart without confirmation |

### 🤖 AI, Logs & Utilities
| Command | Description |
|:--------|:------------|
| `/ask <query>` | Ask Gemini AI about recent log events and system behavior |
| `/report` | Generate a full diagnostic and health report |
| `/logs`, `/logsearch` | View recent system logs or search by pattern |
| `/config`, `/settings` | Inspect settings, change language, or toggle quiet hours |
| `/health` | Healthchecks.io ping status and watchdog history |
| `/backup` | Create a timestamped backup of NASBot configuration |
| `/wol` | Send Wake-on-LAN magic packets to local devices |
| `/update` | Check for and apply the latest GitHub release |

---

## 🔄 Auto-Updates

NASBot features a built-in updater that periodically queries GitHub Releases:
- Automatically downloads compatible binaries for your architecture (`arm64`, `amd64`).
- Verifies checksums, replaces the active binary, and reboots cleanly.
- Sends a confirmation message on Telegram with the version diff upon restart.

---

## 🛡️ Security & Hardening

- **Access Control:** The bot strictly restricts execution to the Telegram ID configured in `allowed_user_id`. All unauthorized messages are dropped.
- **Secret Isolation:** `config.json` is git-ignored and sanitized in logs and configuration previews.
- **Git Hooks:** Install local pre-commit and pre-push quality guards with `./scripts/setup_hooks.sh`.
- Read [docs/SECURITY.md](docs/SECURITY.md) for vulnerability reporting and leak response protocols.

---

## 🧪 Testing & CI/CD

```bash
# Run all tests with race detector
go test -race ./...

# Run the complete CI quality gate locally
./scripts/ci_guard.sh
```

- **CI Pipeline:** Automated formatting, static analysis (`go vet`), race detection, and GitHub release generation with attested build provenance.
- **Full History:** See [docs/CHANGELOG.md](docs/CHANGELOG.md) for release notes.
