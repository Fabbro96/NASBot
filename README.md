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
| 📨 **Flexible Reports** | Configure 0, 1, or 2 daily reports at custom times |
| 🌙 **Quiet Hours** | Customizable silence periods |
| 🛡️ **Autonomous Protection** | Auto-restart containers on critical RAM |
| 🐳 **Docker Management** | Start/Stop/Restart/Kill containers from Telegram || 🌐 **Multi-language** | Support for English and Italian 🇬🇧/🇮🇹 || 🔔 **Smart Alerts** | Fully customizable thresholds per resource |
| 🐳 **Docker Watchdog** | Auto-restart Docker service if unresponsive |
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

NASBot is fully customizable via `config.json`. Copy the example and edit it:

```bash
cp config.example.json config.json
nano config.json
```

### Full Configuration Reference

```json
{
  "bot_token": "YOUR_BOT_TOKEN_HERE",
  "allowed_user_id": 12345678,
  
  "paths": {
    "ssd": "/Volume1",
    "hdd": "/Volume2"
  },
  
  "timezone": "Europe/Rome",
  
  "reports": {
    "enabled": true,
    "morning": {
      "enabled": true,
      "hour": 7,
      "minute": 30
    },
    "evening": {
      "enabled": true,
      "hour": 18,
      "minute": 30
    }
  },
  
  "quiet_hours": {
    "enabled": true,
    "start_hour": 23,
    "start_minute": 30,
    "end_hour": 7,
    "end_minute": 0
  },
  
  "notifications": {
    "cpu": {
      "enabled": true,
      "warning_threshold": 90.0,
      "critical_threshold": 95.0
    },
    "ram": {
      "enabled": true,
      "warning_threshold": 90.0,
      "critical_threshold": 95.0
    },
    "swap": {
      "enabled": false,
      "warning_threshold": 50.0,
      "critical_threshold": 80.0
    },
    "disk_ssd": {
      "enabled": true,
      "warning_threshold": 90.0,
      "critical_threshold": 95.0
    },
    "disk_hdd": {
      "enabled": true,
      "warning_threshold": 90.0,
      "critical_threshold": 95.0
    },
    "disk_io": {
      "enabled": true,
      "warning_threshold": 95.0
    },
    "smart": {
      "enabled": true
    }
  },
  
  "stress_tracking": {
    "enabled": true,
    "duration_threshold_minutes": 2
  },
  
  "docker": {
    "watchdog": {
      "enabled": true,
      "timeout_minutes": 2,
      "auto_restart_service": true
    },
    "weekly_prune": {
      "enabled": true,
      "day": "sunday",
      "hour": 4
    },
    "auto_restart_on_ram_critical": {
      "enabled": true,
      "max_restarts_per_hour": 3,
      "ram_threshold": 98.0
    }
  },
  
  "intervals": {
    "stats_seconds": 5,
    "monitor_seconds": 30,
    "critical_alert_cooldown_minutes": 30
  }
}
```

### Configuration Sections Explained

#### 📨 Reports
- `enabled`: Master switch for periodic reports
- `morning`/`evening`: Configure each report independently
  - Set `enabled: false` for only one daily report
  - Set both to `false` for no automatic reports

#### 🌙 Quiet Hours
- No notifications during these hours (alerts still logged for reports)
- Set `enabled: false` to receive notifications 24/7

#### 🔔 Notifications
Each resource can be independently enabled/disabled:
- **CPU/RAM**: Warning and critical thresholds
- **Swap**: Disabled by default (set `enabled: true` if you care about swap)
- **Disk SSD/HDD**: Space usage thresholds
- **Disk I/O**: High I/O activity threshold
- **SMART**: Disk health monitoring

#### 🐳 Docker
- **Watchdog**: Auto-restart Docker service if no containers for X minutes
  - Set `auto_restart_service: false` to only notify without restarting
  - Set `enabled: false` to disable entirely
- **Weekly Prune**: Clean unused images on specified day/hour
- **Auto-restart on RAM**: Restart heaviest container when RAM is critical

#### ⏱️ Intervals
- `stats_seconds`: How often to collect system stats
- `monitor_seconds`: How often to check for alerts
- `critical_alert_cooldown_minutes`: Minimum time between critical alerts

---

## 🎮 Commands

### 📊 Monitoring
| Command | Description |
|---------|-------------|
| `/status` | Quick system overview with interactive buttons |
| `/temp` | CPU and disk temperatures (requires smartmontools) |
| `/top` | Top processes by CPU usage |
| `/sysinfo` | Detailed system information (OS, kernel, hardware) |
| `/diskpred` | Disk space prediction (estimates when disks will be full) |

### 🐳 Docker
| Command | Description |
|---------|-------------|
| `/docker` | Interactive container management menu |
| `/dstats` | Container resource usage (CPU, RAM, network) |
| `/kill <name>` | Force kill a container (SIGKILL) |
| `/restartdocker` | Restart the Docker daemon |

### 🌐 Network
| Command | Description |
|---------|-------------|
| `/net` | Network information (local and public IP) |
| `/speedtest` | Run a network speed test (requires speedtest-cli) |

### 📋 Reports & System
| Command | Description |
|---------|-------------|
| `/report` | Full detailed report |
| `/ping` | Check if bot is alive (heartbeat) |
| `/config` | Show current configuration |
| `/language` | Change bot language (EN/IT) |
| `/logs` | Recent system logs |
| `/reboot` | Reboot the NAS (requires root) |
| `/shutdown` | Shutdown the NAS (requires root) |
| `/help` | Show all commands |

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

## 🔧 Optional Dependencies

| Package | Purpose | Install |
|---------|---------|---------|
| `smartmontools` | Disk SMART temperatures | `apt install smartmontools` |
| `speedtest-cli` | Network speed test | `apt install speedtest-cli` |

---

## 🛡️ Security Notes

This bot allows executing system commands (`reboot`, `shutdown`, `docker`).

- **Ensure `allowed_user_id` is correctly set in `config.json`**
- The bot will ignore messages from any other Telegram user
- Keep your `config.json` private (it's gitignored by default)
- Consider running as a dedicated user with limited sudo permissions

---

## 🤝 Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

---

## 📄 License

MIT License — see [LICENSE](LICENSE) for details.

---

## 🙏 Acknowledgments

Built with:
- [telegram-bot-api](https://github.com/go-telegram-bot-api/telegram-bot-api) — Telegram Bot API for Go
- [gopsutil](https://github.com/shirou/gopsutil) — Cross-platform system monitoring
