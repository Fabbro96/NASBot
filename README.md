# 🖥️ NASBot

> A lightweight and responsive Telegram bot to monitor your NAS — wherever you are.

![Go](https://img.shields.io/badge/Go-1.18+-00ADD8?logo=go&logoColor=white)
![Platform](https://img.shields.io/badge/Platform-Linux%20ARM64-orange)
![License](https://img.shields.io/badge/License-MIT-green)

---

## ✨ Why NASBot?

Do you have a home NAS or an ARM mini-server and want to check its status **without opening SSH every time**?
NASBot sends you an interactive dashboard on Telegram: CPU, RAM, disks, Docker containers, temperatures — all at your fingertips.

**Key Features:**

| | |
|---|---|
| 📊 **Live Dashboard** | Inline buttons for instant updates |
| 🌅 **Morning Report** | Daily at 07:30 AM with a "Good morning!" status |
| 🌆 **Evening Report** | Daily at 06:30 PM with a "Good evening!" status |
| 🛡️ **Autonomous Protection** | Restarts containers if RAM uses critical |
| 🐳 **Docker Management** | Start/Stop/Restart containers from Telegram |
| 🔔 **Smart Alerts** | Notifies only on prolonged I/O stress (2+ min) |
| 🔄 **Auto-recovery** | Automatic restart after crash/reboot |
| 🔒 **Single Access** | Only your user ID can command the bot |
| 🪶 **Lightweight** | ~6 MB static binary, zero runtime dependencies |

---

## 📋 Requirements

| Requirement | Notes |
|-----------|------|
| **Go ≥ 1.18** | Only if compiling from source |
| **Linux** | Tested on Debian/Ubuntu ARM64, TerraMaster |
| `docker` *(optional)* | For container management |
| `smartmontools` *(optional)* | For SMART temperatures (`/temp`) |

### ⚠️ Permissions

- `/reboot` and `/shutdown` execute `reboot`/`poweroff` directly → the process must run as **root** or have necessary permissions.
- `smartctl` usually requires **root** or membership in the `disk` group.
- For Docker management, the user must be able to execute `docker` commands.

---

## ⚙️ Configuration

1. **Rename** `config.example.json` to `config.json`.
2. **Edit** the file:
   ```json
   {
     "bot_token": "YOUR_234234:ABC...",
     "allowed_user_id": 12345678,
     "paths": {
       "ssd": "/Volume1",
       "hdd": "/Volume2"
     },
     "thresholds": {
       "cpu": 90.0,
       "ram": 90.0,
       "disk": 90.0
     }
   }
   ```
   *   `bot_token`: From @BotFather.
   *   `allowed_user_id`: Your numeric Telegram ID (get it from @userinfobot).
   *   `paths`: Mount points to monitor.
   *   `thresholds`: Alert percentages.

---

## 🚀 Installation

### Option A: Quick Start (Binary)

1. Download the binary (if available) or compile it:
   ```bash
   go build -o nasbot main.go
   ```
2. Make it executable:
   ```bash
   chmod +x nasbot
   ```
3. Run it:
   ```bash
   ./nasbot
   ```

### Option B: Automatic Service (Autostart)

Use the provided script to set up persistence (cron/start script):

```bash
chmod +x setup_autostart.sh
./setup_autostart.sh
```

This will configure a cron job or startup script to keep the bot running.

---

## 🎮 Commands

| Command | Description |
|---|---|
| `/start` | Welcome and main menu |
| `/status` | Complete dashboard (CPU, RAM, Disk, Uptime) |
| `/docker` | Manage containers (List, Start, Stop, Logs) |
| `/temp` | Sensors and Disk temperatures |
| `/reboot` | Reboot the server (requires root) |
| `/shutdown` | Shutdown the server (requires root) |
| `/help` | List of commands |

---

## 🛡️ Security Note

This bot allows executing system commands (`reboot`, `shutdown`, `docker`).
**Ensure `allowed_user_id` is correctly set in `config.json`.**
The bot will ignore messages from any other user.

---

## 📄 License

MIT License. Feel free to fork and modify!
