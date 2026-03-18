# 🖥️ NASBot

> A lightweight Telegram bot to monitor and control your home server/NAS.

![Platform](https://img.shields.io/badge/Platform-Linux%20ARM64-orange)
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

- **📊 Live Stats**: CPU, RAM, Swap, Disk (SSD/HDD), Net, Temperatures.
- **🐳 Docker Manager**: Start, stop, restart, and kill containers via buttons.
- **🤖 AI Reports**: Daily summaries powered by **Gemini 2.5 Flash** (optional).
- **🌍 Multi-language**: EN, IT, ES, DE, ZH, UK (full key coverage with EN fallback).
- **🔔 Smart Alerts**: Notify on high usage, stopped containers, or critical errors.
- **🛡️ Watchdogs**: Auto-restart Docker or containers if they crash or freeze.
- **⚙️ Legacy Config Auto-Heal**: Missing fields in old `config.json` are auto-added with defaults.
- **📨 Reports**: Scheduled summary (morning/evening) with trends and events.

## 🧩 Code Layout (Short)

- `handlers.go`: bot entrypoints (`handleCommand`, `handleCallback`)
- `handlers_callback_routes.go`: callback routing logic (settings, docker/power, scoped handlers)
- `handlers_settings.go`: language + settings keyboards/text helpers
- `config.go`: load/sanitize/patch flow
- `config_defaults.go`: default template + recursive missing-field merge
- `translations.go`: translations + automatic key coverage sync

---

## 🚀 Quick Install

### 1. Download & Install
Run the installer script (works on most Linux ARM64/AMD64 systems):

```bash
# Upload nasbot-arm64, config.json and install.sh to a folder
chmod +x install.sh
sudo ./install.sh
```

### 2. Configuration (`config.json`)
Edit `config.json` with your details.
*You must set at least `bot_token` and `allowed_user_id`.*

```json
{
  "bot_token": "YOUR_TELEGRAM_BOT_TOKEN",
  "allowed_user_id": 12345678,     // Your Telegram User ID
  "gemini_api_key": "",            // Optional: For AI summaries
  "timezone": "Europe/Rome",
  "paths": { "ssd": "/", "hdd": "/mnt/data" } // Paths to monitor
}
```

---

## 🎮 Commands

| Command | Action |
|:---:|---|
| `/status` | 🖥 **Main Dashboard** (Resource usage & interactive menu) |
| `/quick` | ⚡ One-line summary |
| `/docker` | 🐳 Manage containers |
| `/net` | 🌐 Network info (Local/Public IP) |
| `/report` | 📨 Generate full status report now |
| `/settings` | ⚙️ Configure Language, Reports, Quiet Hours |
| `/temp` | 🌡 System temperatures |
| `/reboot` | 🔄 Reboot server |
| `/help` | 📜 Show all commands |

---

## 🤖 AI & Reports

NASBot can use **Google Gemini** to write friendly daily reports ("Everything looks good, but Disk IO was high at 3 AM").
1. Get a key from [Google AI Studio](https://aistudio.google.com/).
2. Add it to `gemini_api_key` in `config.json`.
3. Enjoy human-readable server updates!

---

## ⚙️ Advanced Configuration (Optional)

<details>
<summary>Click to view full config options</summary>

The `config.json` allows granular control over thresholds and automation:

- **Notifications**: Set warning/critical % for CPU, RAM, Disk.
- **Quiet Hours**: Silence notifications at night.
- **Docker Watchdog**: Auto-restart Docker service if it hangs.
- **Auto-Prune**: Weekly cleanup of unused Docker images.

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
Run all tests:

```bash
go test ./...
```

## 🔁 CI/CD

GitHub Actions pipelines:

- `CI` (push/PR): secret scan, `gofmt` check, `go vet`, race tests, build, release script smoke.
- `ShellCheck` (push/PR): strict lint for all `.sh` scripts.
- `Security` (PR + weekly): Dependency Review + CodeQL.
- `Release` (tag `v*`): build binaries, generate checksums, publish GitHub Release artifacts.
- `Dependabot`: weekly updates for `gomod` and GitHub Actions.
