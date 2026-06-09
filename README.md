# 🖥️ NASBot

> A lightweight Telegram bot to monitor and control your home server/NAS.

![Platform](https://img.shields.io/badge/Platform-Linux%20ARM64%20%7C%20AMD64-orange)
![License](https://img.shields.io/badge/License-MIT-green)
[![CI](https://github.com/Fabbro96/NASBot/actions/workflows/ci.yml/badge.svg)](https://github.com/Fabbro96/NASBot/actions/workflows/ci.yml)
[![Security](https://github.com/Fabbro96/NASBot/actions/workflows/security.yml/badge.svg)](https://github.com/Fabbro96/NASBot/actions/workflows/security.yml)
[![Release](https://github.com/Fabbro96/NASBot/actions/workflows/release.yml/badge.svg)](https://github.com/Fabbro96/NASBot/actions/workflows/release.yml)
![Provenance](https://img.shields.io/badge/Release%20Provenance-Attested-blue)

A self-hosted bot that gives you a **live dashboard** (CPU, RAM, Disks, Docker) directly in Telegram. No web UI, just a single binary.

## TL;DR

- NASBot is a self-hosted Telegram bot for your NAS/home server: monitor health, manage Docker, receive alerts, and run quick actions.
- Setup is simple: one binary + one `config.json` (minimum required: `bot_token` and `allowed_user_id`).
- Main daily flow: use `/status` for dashboard, `/quick` for snapshot, `/report` for full report, `/settings` for tuning.
- **Auto-updates**: when a new release is published, NASBot downloads and installs it automatically (configurable).
- Optional AI summaries are available with Gemini by setting `gemini_api_key`.
- Designed for production-style usage: tests, CI, security scans, release artifacts, and update automation are included.

Quick start in ~60 seconds:

```bash
cp config.example.json config.json
# edit config.json: set bot_token and allowed_user_id
go build -o nasbot ./...
./nasbot
```

## ✨ Key Features

- **📊 Live Stats**: CPU, RAM, Swap, Disk (SSD/HDD), Real-Time Network (Mbps), Temperatures.
- **⚙️ Process Manager**: Interactive `/processes` dashboard with inline SIGTERM/SIGKILL buttons.
- **🐳 Docker Manager**: Start, stop, restart, and kill containers via inline buttons.
- **🤖 Self-Healing AI**: Diagnose critical alerts in real-time with **Gemini**, analyzing `syslog` and `top` processes automatically via the `[Analizza con AI]` button.
- **🌍 Multi-language**: EN, IT, ES, DE, ZH, UK (full key coverage with EN fallback).
- **🔔 Smart Alerts**: Notify on high usage, stopped containers, or critical errors.
- **🛡️ Watchdogs**: Network, Kernel, RAID, and Docker watchdogs with auto-recovery.
- **🔄 Auto-Updates**: Checks GitHub releases periodically and downloads new versions. Notifies you on Telegram when an update is applied.
- **⚙️ Legacy Config Auto-Heal**: Missing fields in old `config.json` are auto-added with defaults.
- **📨 Reports**: Scheduled summary (morning/evening) with trends and events.
- **💓 Healthchecks.io**: Built-in integration for uptime monitoring.
- **💾 Backup & WOL**: Integrated tools for NAS configuration backups (`/backup`) and Wake-on-LAN routing.

## 🧩 Code Layout (Short)

- `internal/app/handlers.go`: bot entrypoints (`handleCommand`, `handleCallback`)
- `internal/app/handlers_callback_routes.go`: callback routing logic (settings, docker/power, scoped handlers)
- `internal/app/handlers_settings.go`: language + settings keyboards/text helpers
- `internal/app/config.go`: load/sanitize/patch flow
- `internal/app/config_defaults.go`: default template + recursive missing-field merge
- `internal/app/translations.go`: translations + automatic key coverage sync
- `internal/app/runtime_main.go`: boot sequence, goroutine lifecycle, `goSafe` panic recovery
- `internal/app/updater.go`: auto-update from GitHub releases

---

## 🚀 Quick Install

### Modular Configuration System

**NEW: NASBot now has a fully modular and customizable deployment system!**

All scripts (package, deploy, runtime) use a hierarchical configuration system with environment variable overrides. No script editing needed.

- 📖 **Start here**: [MODULAR_CONFIG_GUIDE.md](MODULAR_CONFIG_GUIDE.md) - Usage guide and examples
- 🏗️ **Deep dive**: [MODULAR_ARCHITECTURE.md](MODULAR_ARCHITECTURE.md) - System design and extensibility
- ⚙️ **Template**: [nasbot.config.template](nasbot.config.template) - Default configuration (documented)
- 📋 **Examples**: [nasbot.config.example](nasbot.config.example) - Real-world scenarios

### 1. Download & Install

The official and **recommended** method to install, run, and maintain the bot is using the provided `start_bot.sh` script.

```bash
chmod +x scripts/start_bot.sh
./scripts/start_bot.sh install
```

> [!WARNING]
> **Do not use `systemd` to run NASBot.**
> Using `systemd` conflicts with the bot's internal auto-update architecture. When the bot attempts to restart itself after an update via `start_bot.sh restart`, `systemd` will detect the killed process and try to restart it simultaneously, resulting in parallel conflicting instances.

Running `./scripts/start_bot.sh install` will automatically:
- Setup a watchdog via `crontab` (runs every 5 minutes to ensure the bot is alive).
- Manage log rotation.
- Start the process cleanly in the background.

### Minimal NAS Runtime (No Source Files)

If you want a clean NAS folder with only runtime essentials (no Go source), build a runtime bundle:

```bash
./scripts/package_runtime.sh --arch arm64
```

This creates `dist/runtime` with only:
- `nasbot`
- `start_bot.sh`
- `config.example.json`

On NAS, keep just these plus runtime-generated files (`nasbot.log`, `nasbot.pid`, `nasbot_state.json`).

### 2. Configuration (`config.json`)
Edit `config.json` with your details.
*You must set at least `bot_token` and `allowed_user_id`.*

```json
{
  "bot_token": "TOKEN",
  "allowed_user_id": 12345678,
  "gemini_api_key": "",
  "timezone": "Europe/Rome",
  "paths": { "ssd": "/", "hdd": "/mnt/data" }
}
```

---

## 🎮 Commands

### 📊 Status & Info
| Command | Action |
|:--------|--------|
| `/status`, `/start` | Stato generale e riepilogo del sistema |
| `/top`, `/processes` | Monitoraggio delle risorse e gestione interattiva dei processi |
| `/sysinfo` | Informazioni dettagliate sull'hardware |
| `/temp` | Temperature hardware (CPU, dischi, ecc.) |

### 🐳 Docker
| Command | Action |
|:--------|--------|
| `/docker` | Menu interattivo di gestione Docker |
| `/dstats`, `/container`, `/restartdocker`, `/kill` | Comandi rapidi per gestione container |

### ⚡ System & Power
| Command | Action |
|:--------|--------|
| `/reboot`, `/shutdown`, `/forcereboot` | Gestione alimentazione NAS |
| `/diskpred` (o `/prediction`) | Previsione esaurimento spazio su disco |
| `/health` (o `/healthchecks`) | Stato dei controlli di salute automatici |
| `/backup` | Backup automatico dei file di configurazione (`config.json`) |
| `/wol` | Invia pacchetto Wake-on-LAN per risvegliare dispositivi locali |
| `/update` | Aggiorna automaticamente il bot scaricando l'ultima release |

### 🌐 Network & Logs
| Command | Action |
|:--------|--------|
| `/net`, `/speedtest` | Stato della rete ed esecuzione speedtest |
| `/ping` | Verifica latenza |
| `/logs`, `/logsearch` | Lettura e ricerca nei log di sistema o del bot |

### 🤖 AI, Config & Tools
| Command | Action |
|:--------|--------|
| `/ask` | Assistente AI (Gemini) per chiedere informazioni |
| `/quick` (o `/q`) | Menu comandi rapidi personalizzati |
| `/report` | Genera un report diagnostico completo del NAS |
| `/config`, `/settings`, `/language` | Gestione delle impostazioni e della lingua |

---

## 🤖 AI & Reports

NASBot can use **Google Gemini** to write friendly daily reports ("Everything looks good, but Disk IO was high at 3 AM").
1. Get a key from [Google AI Studio](https://aistudio.google.com/).
2. Add it to `gemini_api_key` in `config.json`.
3. Enjoy human-readable server updates!

---

## 🔄 Auto-Updates

NASBot includes a built-in updater that periodically checks GitHub releases:

1. **Automatic check**: every 6 hours, NASBot queries the latest release on GitHub.
2. **Auto-apply** (if `update.auto_apply` is `true` in config): downloads and installs the new binary automatically.
3. **Notification**: after restarting, the bot sends a Telegram message: *"✅ Bot updated! `v0.1.3` → `v0.2.0`"*.
4. **Manual trigger**: use `/update` to check and apply updates on demand.

---

## ⚙️ Advanced Configuration (Optional)

<details>
<summary>Click to view full config options</summary>

The `config.json` allows granular control over thresholds and automation:

- **Notifications**: Set warning/critical % for CPU, RAM, Disk.
- **Quiet Hours**: Silence notifications at night.
- **Docker Watchdog**: Auto-restart Docker service if it hangs.
- **Auto-Prune**: Weekly cleanup of unused Docker images.
- **Network Watchdog**: Force reboot if network is down for too long.
- **Kernel Watchdog**: Detect OOM kills, kernel panics, hung tasks.
- **RAID Watchdog**: Alert on degraded RAID arrays.
- **Healthchecks.io**: External uptime monitoring integration.

See `config.example.json` for the full schema.
</details>

## 🛡️ Security
This bot executes commands like `docker` and `reboot`. Ensure `allowed_user_id` is correct. The bot ignores all other users.

`config.json` is ignored by git on purpose. Keep API keys and tokens there locally, and rotate them if they ever leak.

Enable local hardening hooks:

```bash
git config core.hooksPath .githooks
chmod +x .githooks/pre-commit scripts/secret_scan.sh
```

See [docs/SECURITY.md](docs/SECURITY.md) for full hardening policy and leak response steps.

## 🧪 Testing
Run all tests (with race detector):

```bash
go test -race ./...
```

## 🔁 CI/CD

GitHub Actions pipelines:

- **CI** (push/PR): secret scan, `gofmt` check, `go vet`, race tests, build, release script smoke.
- **Security** (PR + weekly): Dependency Review + CodeQL.
- **Release** (push to `main` or tag `v*`): auto-tag via [github-tag-action](https://github.com/mathieudutour/github-tag-action), build ARM64/AMD64 binaries, generate checksums, attest provenance, publish GitHub Release.
- **Dependabot**: weekly updates for `gomod` and GitHub Actions.
