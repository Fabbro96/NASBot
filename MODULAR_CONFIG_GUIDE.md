# NASBot Modular Configuration System

**Lingua italiana in fondo**

## Overview

The NASBot deployment system is now fully modular and customizable. Every script uses a hierarchical configuration system that allows unlimited customization through:

1. **Default values** (`scripts/common.sh`)
2. **Configuration files** (`nasbot.config.local`, `nasbot.config.template`)
3. **Environment variables** (`NASBOT_*` prefix)
4. **Command-line flags**

This means each user can adapt the system to their specific needs without modifying scripts.

## Configuration Hierarchy (Priority Order)

```
Environment Variables (NASBOT_*)
         ↑
    Command-line Flags
         ↑
   nasbot.config.local (local file)
         ↑
   nasbot.config.template (default template)
         ↑
   Hard-coded Defaults (in scripts)
```

## Quick Start

### 1. Initialize Configuration

```bash
# Copy template to local config
cp nasbot.config.template nasbot.config.local

# Edit to customize your environment
nano nasbot.config.local
```

### 2. Simple Usage

```bash
# Package runtime with defaults
./scripts/package_runtime.sh

# Deploy to NAS
./scripts/deploy_runtime_rsync.sh --target user@nas:/Volume1/public

# Start bot with default config
./start_bot.sh start
```

## Advanced Configuration Examples

### Example 1: ARM64 Build with Custom Output

```bash
# Using environment variables
export NASBOT_BUILD_ARCH=arm64
export NASBOT_PACKAGE_OUT_DIR=/tmp/nasbot-arm
./scripts/package_runtime.sh

# Or using config file
cat > nasbot.config.local <<EOF
BUILD_ARCH="arm64"
PACKAGE_OUT_DIR="/tmp/nasbot-arm"
EOF

./scripts/package_runtime.sh --config nasbot.config.local
```

### Example 2: Deploy to Multiple NAS Locations

```bash
# NAS #1 - Development
./scripts/deploy_runtime_rsync.sh \
  --target dev@nas-dev:/share/nasbot \
  --dry-run

# NAS #2 - Production (with cleanup)
./scripts/deploy_runtime_rsync.sh \
  --target prod@nas-prod:/data/bot \
  --arch arm64 \
  --delete

# NAS #3 - Local testing
./scripts/deploy_runtime_rsync.sh \
  --target /mnt/test-nfs/nasbot
```

### Example 3: Runtime with Custom Paths

```bash
# Use custom log/pid/state locations
export NASBOT_LOG_FILE=/var/log/nasbot/app.log
export NASBOT_PID_FILE=/var/run/nasbot.pid
export NASBOT_STATE_FILE=/var/lib/nasbot/state.json

./start_bot.sh start
./start_bot.sh watch
```

### Example 4: Custom Build Flags and Version

```bash
cat > nasbot.config.local <<EOF
BUILD_ARCH="amd64"
BUILD_FLAGS="-trimpath"
VERSION="v2.1.0-prod"
PACKAGE_OUT_DIR="./dist/prod-release"
EOF

./scripts/package_runtime.sh --config nasbot.config.local
```

## Configuration Reference

### Build Settings

```bash
# Go version (auto-detect if empty)
GO_VERSION=""

# Architecture: native, arm64, amd64, armv7, 386
BUILD_ARCH="native"

# Additional go build flags
BUILD_FLAGS=""
```

### Runtime Settings

```bash
# Application name
APP_NAME="nasbot"

# Config file path
CONFIG_FILE="config.json"

# Config example file path
CONFIG_EXAMPLE="config.example.json"
```

### Logging & State

```bash
# File paths (absolute or relative to runtime dir)
LOG_FILE="nasbot.log"
PID_FILE="nasbot.pid"
STATE_FILE="nasbot_state.json"

# Log rotation
LOG_MAX_SIZE_MB=10
LOG_BACKUP_COUNT=5
```

### Deployment Settings

```bash
# Default deployment target
DEPLOY_TARGET=""

# Deployment credentials
DEPLOY_USER=""
DEPLOY_HOST=""
DEPLOY_PATH="/Volume1/public"

# Rsync behavior
USE_RSYNC="true"
RSYNC_OPTS="--compress --progress"

# Protected files (never deleted)
RSYNC_EXCLUDE_DELETE="config.json:nasbot.log:nasbot.pid:nasbot_state.json:#recycle"
```

### Packaging Settings

```bash
# Output directory
PACKAGE_OUT_DIR="./dist"

# Include example config in package
INCLUDE_EXAMPLE_CONFIG="true"

# Include runtime README
INCLUDE_README_RUNTIME="true"

# Additional files to include (colon-separated)
ADDITIONAL_FILES=""

# Binary name
BINARY_NAME="nasbot"
```

### Watchdog Settings

```bash
WATCHDOG_ENABLED="false"
WATCHDOG_BINARY="fswatchdog"
WATCH_PATH="/mnt/nas"
```

### Update Settings

```bash
# Check for updates on startup
CHECK_UPDATES="true"

# Update check interval (seconds)
UPDATE_CHECK_INTERVAL=3600

# Update file pattern (must match exactly)
UPDATE_FILE_PATTERN="nasbot-update*"

# Auto-restart after update
AUTO_RESTART_ON_UPDATE="true"

# Restart script path
RESTART_SCRIPT="./start_bot_runtime.sh"
```

### Performance & Debug

```bash
# Verbose logging
VERBOSE="false"

# Debug mode
DEBUG="false"

# Number of parallel workers
PARALLEL_WORKERS=""

# Cache directory
CACHE_DIR="./cache"
```

## Common Use Cases

### Use Case 1: Multi-Environment Setup

```bash
# Create environment-specific configs
cat > nasbot.config.dev <<EOF
BUILD_ARCH="native"
DEPLOY_TARGET="dev@nas-dev:/share"
VERBOSE="true"
DEBUG="true"
EOF

cat > nasbot.config.prod <<EOF
BUILD_ARCH="arm64"
DEPLOY_TARGET="prod@nas-prod:/data"
VERBOSE="false"
LOG_MAX_SIZE_MB=50
EOF

# Use them selectively
./scripts/package_runtime.sh --config nasbot.config.dev
./scripts/deploy_runtime_rsync.sh --config nasbot.config.prod --delete
```

### Use Case 2: CI/CD Integration

```bash
# In GitHub Actions or similar
env:
  NASBOT_BUILD_ARCH: "arm64"
  NASBOT_VERSION: "${{ github.ref }}"
  NASBOT_VERBOSE: "true"

run: |
  ./scripts/package_runtime.sh
  ./scripts/deploy_runtime_rsync.sh --target ${{ secrets.NAS_TARGET }}
```

### Use Case 3: Automated Updates

```bash
#!/bin/bash
# deploy-latest.sh
set -e

VERSION=$(curl -s https://api.github.com/repos/user/nasbot/releases/latest | jq -r '.tag_name')
export NASBOT_VERSION="${VERSION}"

./scripts/package_runtime.sh
./scripts/deploy_runtime_rsync.sh \
  --target "$DEPLOY_HOST" \
  --delete \
  --dry-run

# Review output, then:
./scripts/deploy_runtime_rsync.sh --target "$DEPLOY_HOST" --delete
```

## Scripting Helpers From `common.sh`

The `common.sh` script provides utilities for custom scripts:

```bash
#!/bin/bash
source "scripts/common.sh"

# Load configuration
load_config "custom.config"

# Use helpers
log_info "Starting deployment..."
require_cmd rsync
validate_arch "${BUILD_ARCH}"

# Resolve paths with env var support
log_file=$(resolve_runtime_path "LOG_FILE" "/var/log")
echo "Log file: ${log_file}"

# Get architecture info
go_arch=$(get_go_arch "arm64")
echo "GOOS:GOARCH = ${go_arch}"
```

## Environment Variable Reference

All settings can be overridden via `NASBOT_` prefixed environment variables:

```bash
# Examples
export NASBOT_BUILD_ARCH="arm64"
export NASBOT_PACKAGE_OUT_DIR="/custom/path"
export NASBOT_LOG_FILE="/var/log/nasbot.log"
export NASBOT_VERBOSE="true"
export NASBOT_USE_RSYNC="false"
export NASBOT_DELETE_EXTRA="true"
```

## Troubleshooting

### Config Not Loaded?

1. Check file exists: `ls -la nasbot.config.local`
2. Verify syntax: `source nasbot.config.local` (should not error)
3. Check environment: `echo $NASBOT_BUILD_ARCH` (shows env override)
4. Enable verbose: `./scripts/package_runtime.sh --verbose`

### Deployment Fails?

1. Test with dry-run first: `--dry-run`
2. Check rsync options: `NASBOT_RSYNC_OPTS="-vv"` for debug output
3. Verify paths: `echo $NASBOT_DEPLOY_TARGET`
4. Test connection: `ssh user@host ls -la /path`

### Need More Help?

1. Run with verbose: `--verbose` flag
2. Check script: `bash -x scripts/package_runtime.sh`
3. View config: `./start_bot.sh config`

---

# Versione Italiana

## Panoramica

Il sistema di deployment NASBot è ora completamente modulare e personalizzabile. Ogni script usa un sistema di configurazione gerarchico che permette personalizzazione illimitata tramite:

1. **Valori predefiniti** (`scripts/common.sh`)
2. **File di configurazione** (`nasbot.config.local`, `nasbot.config.template`)
3. **Variabili d'ambiente** (prefisso `NASBOT_`)
4. **Flag da linea di comando**

## Gerarchia di Configurazione (Ordine di Priorità)

```
Variabili d'Ambiente (NASBOT_*)
         ↑
    Flag da linea di comando
         ↑
   nasbot.config.local (file locale)
         ↑
   nasbot.config.template (template predefinito)
         ↑
   Valori Hard-coded (negli script)
```

## Inizio Rapido

### 1. Inizializzare la Configurazione

```bash
# Copia template in config locale
cp nasbot.config.template nasbot.config.local

# Modifica per personalizzare il tuo ambiente
nano nasbot.config.local
```

### 2. Uso Semplice

```bash
# Package runtime con impostazioni predefinite
./scripts/package_runtime.sh

# Deploya su NAS
./scripts/deploy_runtime_rsync.sh --target user@nas:/Volume1/public

# Avvia bot con config predefinita
./start_bot.sh start
```

## Configurazione Avanzata

### Esempio 1: Build ARM64 con Output Personalizzato

```bash
cat > nasbot.config.local <<EOF
BUILD_ARCH="arm64"
PACKAGE_OUT_DIR="/tmp/nasbot-arm"
LOG_MAX_SIZE_MB=20
EOF

./scripts/package_runtime.sh --config nasbot.config.local
```

### Esempio 2: Deploya su Più Locazioni NAS

```bash
# NAS #1 - Sviluppo
./scripts/deploy_runtime_rsync.sh \
  --target dev@nas-dev:/share/nasbot \
  --dry-run

# NAS #2 - Produzione (con pulizia)
./scripts/deploy_runtime_rsync.sh \
  --target prod@nas-prod:/data/bot \
  --arch arm64 \
  --delete

# NAS #3 - Testing locale
./scripts/deploy_runtime_rsync.sh \
  --target /mnt/test-nfs/nasbot
```

### Esempio 3: Runtime con Percorsi Personalizzati

```bash
# Usa locazioni custom per log/pid/state
export NASBOT_LOG_FILE=/var/log/nasbot/app.log
export NASBOT_PID_FILE=/var/run/nasbot.pid
export NASBOT_STATE_FILE=/var/lib/nasbot/state.json

./start_bot.sh start
./start_bot.sh watch
```

## Comandi Script Disponibili

### package_runtime.sh

```bash
# Opzioni disponibili
--arch ARCH              # Architettura: native, arm64, amd64, armv7, 386
--out-dir DIR            # Directory di output
--version VERSION        # Versione applicazione
--config FILE            # File di config personalizzato
--include-examples       # Includi config.example.json
--add-files PATHS        # File aggiuntivi (separati da :)
--verbose                # Output verbose
```

### deploy_runtime_rsync.sh

```bash
# Opzioni disponibili
--target TARGET          # Destinazione remota (obbligatorio)
--arch ARCH              # Architettura build
--dry-run                # Simulazione senza modifiche
--delete                 # Elimina file remoti non in bundle
--rsync-opts OPTS        # Opzioni rsync custom
--exclude LIST           # File mai da eliminare
--verbose                # Output verbose
```

### start_bot.sh

```bash
# Comandi disponibili
./start_bot.sh start     # Avvia bot
./start_bot.sh stop      # Ferma bot
./start_bot.sh restart   # Riavvia bot
./start_bot.sh status    # Mostra stato dettagliato
./start_bot.sh logs [N]  # Ultimi N log (default: 50)
./start_bot.sh watch     # Monitora log in real-time
./start_bot.sh config    # Mostra/inizializza configurazione
./start_bot.sh install   # Configura auto-restart via cron
```

## Variabili d'Ambiente

Tutti i setting possono essere sovrascritti con variabili `NASBOT_*`:

```bash
export NASBOT_BUILD_ARCH="arm64"
export NASBOT_PACKAGE_OUT_DIR="/custom/path"
export NASBOT_LOG_FILE="/var/log/nasbot.log"
export NASBOT_VERBOSE="true"
```

Ogni utente può quindi adattare il sistema alle proprie esigenze specifiche!
