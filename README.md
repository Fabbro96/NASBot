# 🖥️ NASBot

> A lightweight, bilingual (EN/IT) Telegram bot to monitor your home server/NAS.

![Platform](https://img.shields.io/badge/Platform-Linux%20ARM64-orange)
![License](https://img.shields.io/badge/License-MIT-green)

A self-hosted bot that gives you a **live dashboard** of your system (CPU, RAM, Disks, Docker) directly in Telegram. No web interface, no complex setup, just a single binary.

## ✨ Key Features

- **📊 Live Stats**: CPU, RAM, Swap, Disk (SSD/HDD), Net, Temperatures.
- **🐳 Docker Manager**: Start, stop, restart, and kill containers via buttons.
- **🤖 AI Reports**: Daily summaries powered by **Gemini 3.0** (optional).
- **🌍 Bilingual**: Full support for **English** 🇬🇧 and **Italian** 🇮🇹.
- **🔔 Smart Alerts**: Notify on high usage, stopped containers, or critical errors.
- **🛡️ Watchdogs**: Auto-restart Docker or containers if they crash or freeze.
- **📨 Reports**: Scheduled summary (morning/evening) with trends and events.

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
