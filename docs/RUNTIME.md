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

### Bundle Structure
```text
dist/runtime/
├── nasbot                  # Compiled binary
├── start_bot.sh            # Production process runner & supervisor
├── config.example.json     # Configuration template
├── nasbot.config.template  # Optional deployment environment overrides
└── README_RUNTIME.md       # Quick-reference guide
```

---

## 2. Deploying to your NAS

### Option A: Direct copy (SCP / SFTP / Rsync)
```bash
rsync -avz dist/runtime/ user@your-nas:/opt/nasbot/
```

### Option B: Automated deployment script
```bash
./scripts/deploy_runtime_rsync.sh user@your-nas:/opt/nasbot/
```

---

## 3. Initial Setup on NAS

SSH into your NAS and navigate to the deployment folder:

```bash
cd /opt/nasbot

# 1. Create and edit your config
cp config.example.json config.json
nano config.json   # set bot_token and allowed_user_id

# 2. Make scripts executable
chmod +x start_bot.sh nasbot

# 3. Install crontab watchdog & start the bot
./start_bot.sh install
```

> [!WARNING]
> **Do not use `systemd` to supervise NASBot.**
> NASBot manages its own process lifecycle and automatic self-updates via `start_bot.sh`. Supervised systemd units conflict with in-place binary upgrades, causing dual-instance races.

---

## 4. Managing the Bot

| Command | Action |
|:--------|:-------|
| `./start_bot.sh start` | Start bot in background |
| `./start_bot.sh stop` | Gracefully stop the bot |
| `./start_bot.sh restart` | Restart bot (detects update binaries) |
| `./start_bot.sh status` | Check process status and PID |
| `./start_bot.sh logs` | View recent application logs |
| `./start_bot.sh watch` | Stream logs live (`tail -f`) |

---

## 5. Updates

### Automatic Updates (Recommended)
If `update.auto_apply` is enabled in `config.json`, NASBot automatically checks GitHub releases, downloads the new version, replaces the binary, and restarts itself cleanly.

### Manual Update
To apply a binary update manually:
1. Drop the new binary into the folder as `nasbot-update` (or `nasbot-update-arm64` / `nasbot-update-amd64`).
2. Run:
   ```bash
   ./start_bot.sh restart
   ```
3. The script detects `nasbot-update`, backs up the old binary, replaces it, and restarts.

---

## 6. Runtime-Generated Files

Once running, NASBot automatically creates and manages:
- `nasbot.log`: Application logging (managed with rotation)
- `nasbot.pid`: Active process ID
- `nasbot_state.json`: State cache (statistics, trends, quiet hours settings)

---

## 7. Troubleshooting

- **Bot doesn't start:**
  - Verify `config.json` is valid JSON and contains valid `bot_token` and `allowed_user_id`.
  - Check `start_bot.sh logs` or `cat nasbot.log`.
- **Permission errors:**
  - Run `chmod +x start_bot.sh nasbot`.
- **Process already running:**
  - Run `./start_bot.sh stop` or inspect with `./start_bot.sh status`.
