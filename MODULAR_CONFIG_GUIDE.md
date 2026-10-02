# NASBot Modular Configuration System

**Lingua italiana in fondo**

## Overview

The scripts in `scripts/` resolve their settings from a hierarchy. Before reading the
rest of this guide, note two facts that the naive "flags win" story gets wrong:

1. **`--config FILE` re-runs the whole hierarchy while it is parsed**, so it
   overwrites whatever the flags *before* it had set. Argument order matters.
2. **A `nasbot.config.local` is only read when you pass `--config`.** The scripts
   source `common.sh`, which auto-loads the config file, and then immediately
   re-initialise their own variables from the environment, throwing the file's values
   away. Only `--config` brings them back.

Everything below is written against the scripts as they are today. If a key is listed
here, some script reads it; keys that used to be documented but were never read have
been removed (see [Removed Keys](#removed-keys)).

## Configuration Hierarchy (How It Really Resolves)

When `--config FILE` is parsed, `load_config FILE` applies, in order:

```
1. DEFAULTS array in scripts/common.sh
2. nasbot.config.template            (repository root, always present)
3. FILE                             (your config, if --config was given)
4. NASBOT_* environment variables    (only for keys listed in DEFAULTS)
```

`--config` therefore **overwrites flags parsed before it** and is **overwritten by
flags parsed after it**:

```bash
# cfg.conf contains: BUILD_ARCH="amd64"

./scripts/package_runtime.sh --arch arm64 --config cfg.conf
#   -> nasbot-amd64   config file beat the flag
./scripts/package_runtime.sh --config cfg.conf --arch arm64
#   -> nasbot-arm64   the flag came after, so it won
```

Verified on an x86_64 host with `scripts/package_runtime.sh`, inspecting the name of
the arch-suffixed binary in the resulting bundle.

Practical rules:

- Put `--config` **before** the flags you care about.
- Prefer `NASBOT_*` environment variables over a config file when you also pass flags:
  env vars are read both when the script initialises itself and inside `load_config`.
- Environment variables beat values in the file, but a flag passed **after** `--config`
  beats the environment.

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
./scripts/deploy_runtime_rsync.sh --target user@nas:/Volume1/nasbot

# Start the bot (source checkout)
./scripts/start_bot.sh start
```

## Configuration Reference

Only keys that are actually read by a script appear here.

### Build Settings (`package_runtime.sh`, `deploy_runtime_rsync.sh`)

```bash
# Architecture: native, arm64, amd64, armv7, 386
BUILD_ARCH="native"

# Version string stamped into the binary via -ldflags
VERSION="dev"

# Binary name (without extension)
BINARY_NAME="nasbot"
```

There is no `GO_VERSION`: the scripts use whatever `go` is on `PATH`. There is no
`BUILD_FLAGS` either — `go build` is invoked with a fixed flag set
(`-ldflags "-X main.Version=..."`) and no key adds to it.

### Deployment Settings (`deploy_runtime_rsync.sh`)

```bash
# Default deployment target. --target is still required unless this is set
# AND --config re-loads it.
DEPLOY_TARGET=""

# Additional rsync options (a string, split on whitespace)
RSYNC_OPTS="--compress --progress"

# Colon-separated names that --delete never removes
RSYNC_EXCLUDE_DELETE="config.json:nasbot.log:nasbot.pid:nasbot_state.json:#recycle"
```

`DEPLOY_USER`, `DEPLOY_HOST` and `DEPLOY_PATH` are **not** read: the target is a single
string written whole by `--target`. `USE_RSYNC` is not read either — rsync is always
the transport.

### Packaging Settings (`package_runtime.sh`)

```bash
# Output directory (script default: ./dist/runtime)
PACKAGE_OUT_DIR="./dist/runtime"

# Include config.example.json in the bundle
INCLUDE_EXAMPLE_CONFIG="true"

# Include README_RUNTIME.md in the bundle
INCLUDE_README_RUNTIME="true"

# Additional files to include (colon-separated paths relative to the repo root)
ADDITIONAL_FILES=""
```

### Verbose Logging

```bash
VERBOSE="false"
```

The `--verbose` flag sets the same thing.

### Values That Are Only Runtime Defaults

These live in the `DEFAULTS` array of `common.sh` and are **not consumed** by any script,
so they were removed from `nasbot.config.template`:

- `TZ` and `UMASK` are loaded and then ignored — no script runs `umask` and none sets
  the timezone. The bot's timezone comes from the `timezone` field of `config.json`.
- `CONFIG_FILE`, `CONFIG_EXAMPLE` and `CHECK_UPDATES` are never read. The bundle always
  looks for `config.json` / `config.example.json` by name, next to itself.
- `DEPLOY_PATH` is never read: the target is a single string written whole by `--target`.
- `LOG_FILE` / `PID_FILE` / `STATE_FILE` are fixed per runner. `scripts/start_bot.sh`
  hardcodes `bin/` and `var/` (lines 6-13) and **exports** `NASBOT_LOG_FILE`,
  `NASBOT_PID_FILE`, `NASBOT_STATE_FILE` to the bot it starts; the bundle's
  `start_bot.sh` *reads* those names from the environment. See
  [Runtime Bundle Environment](#runtime-bundle-environment).
- `APP_NAME`, `AUTO_RESTART_ON_UPDATE`, `UPDATE_FILE_PATTERN`, `ULIMIT_N`, `DEBUG` and
  `LOG_MAX_SIZE_MB` are read by the bundle's runner **only** as `NASBOT_*` environment
  variables, never from a config file. They remain in the table below; they are gone from
  the template because a file could not set them anyway.

## Runtime Bundle Environment

The runtime bundle shipped to the NAS (`start_bot.sh` = `scripts/start_bot_runtime.sh`)
reads its settings from **environment variables only** — it sources `common.sh` with
`NASBOT_NO_AUTO_LOAD=1`, so no config file is loaded at all:

| Variable | Default | Effect |
|:---------|:--------|:-------|
| `NASBOT_APP_NAME` | `nasbot` | Process name used by `pgrep` |
| `NASBOT_BINARY_PATH` | `<bundle>/nasbot` | Binary to run |
| `NASBOT_LOG_FILE` | `<bundle>/nasbot.log` | Log path |
| `NASBOT_PID_FILE` | `<bundle>/nasbot.pid` | PID/lock file |
| `NASBOT_STATE_FILE` | `<bundle>/nasbot_state.json` | State file |
| `NASBOT_LOG_MAX_SIZE_MB` | `10` | Rotation threshold |
| `NASBOT_AUTO_RESTART_ON_UPDATE` | `true` | Apply a pending `nasbot-update*` on start |
| `NASBOT_UPDATE_FILE_PATTERN` | `nasbot-update*` | Glob for pending updates |
| `NASBOT_ULIMIT_N` | unset | `ulimit -n` for the bot |
| `NASBOT_DEBUG` | `false` | Debug output |
| `NASBOT_VERBOSE` | `false` | Verbose output |

```bash
NASBOT_LOG_FILE=/var/log/nasbot/app.log \
NASBOT_LOG_MAX_SIZE_MB=50 \
  ./start_bot.sh start
```

### Which `start_bot.sh`?

There are two different runners with two different subcommand sets:

| Subcommand | `scripts/start_bot.sh` (source checkout) | Bundle's `start_bot.sh` (on the NAS) |
|:-----------|:------------------------------------------|:--------------------------------------|
| `start` / `stop` / `restart` / `status` | yes | yes |
| `logs [n]` | yes | yes |
| `watchdog` | yes | yes |
| `install` | yes | yes |
| `watch` | **no** | yes |
| `config`, `config init` | **no** | yes |
| file locations | `bin/`, `var/` | bundle directory |

`./start_bot.sh watch` and `./start_bot.sh config` exist **only in the bundle**. With
the source runner they fall through to `usage` and exit without doing anything.

## Removed Keys

`nasbot.config.template` used to promise 45 keys. **35 of them were removed** because no
script reads them; the 11 that remain are the ones documented above.

Nothing at all reads these 23, as a file key or as an environment variable:

`GO_VERSION`, `BUILD_FLAGS`, `DEPLOY_USER`, `DEPLOY_HOST`, `DEPLOY_PATH`,
`USE_RSYNC`, `WATCHDOG_ENABLED`, `WATCHDOG_BINARY`, `WATCH_PATH`,
`UPDATE_CHECK_INTERVAL`, `RESTART_SCRIPT`, `AUTO_APPLY_RELEASES`, `PARALLEL_WORKERS`,
`CACHE_DIR`, `NOTIFICATIONS_ENABLED`, `NOTIFICATION_ENDPOINT`, `HEALTH_CHECK_ENABLED`,
`HEALTH_CHECK_PORT`, `NETWORK_INTERFACE`, `CUSTOM_ENV`, `LOG_BACKUP_COUNT`, `TZ`,
`UMASK`.

The other 12 (`APP_NAME`, `CONFIG_FILE`, `CONFIG_EXAMPLE`, `LOG_FILE`, `PID_FILE`,
`STATE_FILE`, `CHECK_UPDATES`, `UPDATE_FILE_PATTERN`, `AUTO_RESTART_ON_UPDATE`,
`ULIMIT_N`, `DEBUG`, `LOG_MAX_SIZE_MB`) are covered by
[Values That Are Only Runtime Defaults](#values-that-are-only-runtime-defaults).

Two areas that this file used to pretend to configure are configured somewhere else
entirely:

- **Watchdog.** The filesystem watchdog runs *inside* the bot; there is no
  `WATCHDOG_BINARY` process to supervise. Configure it with the `fs_watchdog` section
  of `config.json`: `enabled`, `check_interval_minutes`, `warning_threshold`,
  `critical_threshold`, `deep_scan_paths`, `exclude_patterns`.
- **Updates.** Configured with the `update` section of `config.json`
  (`auto_apply`, `check_interval_hours`), not with shell keys. See
  [docs/RUNTIME.md](docs/RUNTIME.md) for the two conflicting defaults.

## Common Use Cases

### Use Case 1: Multi-Environment Setup

```bash
# Create environment-specific configs
cat > nasbot.config.dev <<EOF
BUILD_ARCH="native"
PACKAGE_OUT_DIR="./dist/dev"
VERBOSE="true"
EOF

cat > nasbot.config.prod <<EOF
BUILD_ARCH="arm64"
DEPLOY_TARGET="prod@nas-prod:/data/nasbot"
VERBOSE="false"
EOF

# --config FIRST, then the flags you want to keep
./scripts/package_runtime.sh --config nasbot.config.dev
./scripts/package_runtime.sh --config nasbot.config.prod --out-dir ./dist/prod
./scripts/deploy_runtime_rsync.sh --config nasbot.config.prod --delete --yes
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
  ./scripts/deploy_runtime_rsync.sh --target "$NAS_TARGET" --delete --yes
```

### Use Case 3: Automated Updates

```bash
#!/bin/bash
# deploy-latest.sh
set -euo pipefail

export NASBOT_VERSION="$(curl -s https://api.github.com/repos/user/nasbot/releases/latest | jq -r '.tag_name')"
export NASBOT_DEPLOY_TARGET="$DEPLOY_HOST"

./scripts/package_runtime.sh

# Preview first: --dry-run deletes nothing and needs no confirmation
./scripts/deploy_runtime_rsync.sh --dry-run

# Then apply. --delete on an absolute path needs a terminal or --yes.
./scripts/deploy_runtime_rsync.sh --delete --yes
```

## Scripting Helpers From `common.sh`

The `common.sh` script provides utilities for custom scripts:

```bash
#!/bin/bash
source "scripts/common.sh"

# Re-load with an explicit file (again: this overrides earlier flags)
load_config "custom.config"

log_info "Starting deployment..."
require_cmd rsync
validate_arch "${BUILD_ARCH}"

# Absolute path for a relative config value
echo "$(resolve_path "nasbot.log" "/opt/nasbot")"

# GOOS:GOARCH for an architecture name
echo "$(get_go_arch arm64)"   # linux:arm64
```

Two notes on the helpers:

- `load_config` sources `nasbot.config.template` through
  `grep -E '^\s*[A-Z_]+='`, so the file must be plain `KEY="value"` lines. Anything else
  is silently ignored.
- `resolve_runtime_path "LOG_FILE" ...` still exists in `common.sh`, but since no script
  reads `LOG_FILE` it will not tell you where the bot actually logs.

## Environment Variable Reference

`load_config` applies `NASBOT_*` overrides only for the keys present in the `DEFAULTS`
array of `common.sh`. Variables a script reads directly are always honoured:

```bash
# Read by package_runtime.sh / deploy_runtime_rsync.sh
export NASBOT_BUILD_ARCH="arm64"
export NASBOT_PACKAGE_OUT_DIR="./dist/arm"
export NASBOT_VERSION="v1.6.2"
export NASBOT_BINARY_NAME="nasbot"
export NASBOT_INCLUDE_EXAMPLE_CONFIG="true"
export NASBOT_INCLUDE_README_RUNTIME="true"
export NASBOT_ADDITIONAL_FILES="docs/RUNTIME.md:LICENSE"
export NASBOT_VERBOSE="true"

export NASBOT_DEPLOY_TARGET="user@nas:/Volume1/nasbot"
export NASBOT_RSYNC_OPTS="-vv"
export NASBOT_RSYNC_EXCLUDE_DELETE="config.json:nasbot_state.json"
export NASBOT_BUNDLE_DIR="dist/runtime-deploy"
export NASBOT_DELETE_EXTRA="true"
export NASBOT_ASSUME_YES="true"
export NASBOT_DRY_RUN="true"

# Read only by the bundle's start_bot.sh
export NASBOT_LOG_FILE="/var/log/nasbot/app.log"
export NASBOT_PID_FILE="/run/nasbot.pid"
export NASBOT_STATE_FILE="/var/lib/nasbot/state.json"
```

`NASBOT_USE_RSYNC` does nothing: rsync is not optional.

## Troubleshooting

### Config Not Loaded?

1. Did you pass `--config`? Without it, `nasbot.config.local` is auto-loaded and then
   overwritten by the script's own initialisation.
2. Is `--config` **before** the flags you want to keep?
3. Check the file exists: `ls -la nasbot.config.local`
4. Verify syntax: `bash -n nasbot.config.local`
5. Enable verbose and look for the `Env override:` lines:
   `./scripts/package_runtime.sh --verbose --config nasbot.config.local`

### Deployment Fails?

1. `--target` is required and has no positional form: see [docs/RUNTIME.md](docs/RUNTIME.md).
2. Test with dry-run first: `--dry-run`
3. Check rsync options: `NASBOT_RSYNC_OPTS="-vv"` for debug output
4. Verify paths: `echo "${NASBOT_DEPLOY_TARGET}"`
5. Test connection: `ssh user@host ls -la /path`

### Need More Help?

1. Run with verbose: `--verbose` flag
2. Trace it: `bash -x scripts/package_runtime.sh --config nasbot.config.local`
3. Inspect the bundle runner's view of its own settings (on the NAS only):
   `./start_bot.sh config`

---

# Versione Italiana

## Panoramica

Gli script in `scripts/` risolvono le impostazioni tramite una gerarchia. Prima di
leggere il resto, due fatti che la versione "i flag vincono sempre" sbaglia:

1. **`--config FILE` riesegue tutta la gerarchia mentre viene parsato**, quindi
   sovrascrive i flag impostati **prima** di lui. L'ordine degli argomenti conta.
2. **Un `nasbot.config.local` viene letto solo se passi `--config`.** Lo script
   carica `common.sh`, che auto-carica il file, e subito dopo riapplica le proprie
   variabili dall'ambiente, scartando i valori del file. Solo `--config` li recupera.

## Gerarchia di Configurazione (Come Risolve Davvero)

Quando viene parsato `--config FILE`, `load_config FILE` applica nell'ordine:

```
1. Array DEFAULTS in scripts/common.sh
2. nasbot.config.template            (radice del repository, sempre presente)
3. FILE                             (il tuo file, se hai passato --config)
4. Variabili d'ambiente NASBOT_*    (solo per le chiavi presenti in DEFAULTS)
```

Quindi `--config` **sovrascrive i flag precedenti** ed è **sovrascritto dai flag
successivi**:

```bash
# cfg.conf contiene: BUILD_ARCH="amd64"

./scripts/package_runtime.sh --arch arm64 --config cfg.conf
#   -> nasbot-amd64   il file ha vinto sul flag
./scripts/package_runtime.sh --config cfg.conf --arch arm64
#   -> nasbot-arm64   il flag viene dopo, quindi ha vinto
```

Verificato su host x86_64 con `scripts/package_runtime.sh`, guardando il nome del
binario con suffisso di architettura nel bundle risultante.

Regole pratiche:

- Metti `--config` **prima** dei flag che ti servono.
- Se passi anche dei flag, preferisci le variabili `NASBOT_*`: vengono lette sia
  all'inizializzazione dello script sia dentro `load_config`.
- Le variabili d'ambiente battono il file, ma un flag messo **dopo** `--config`
  batte l'ambiente.

## Comandi Script Disponibili

### package_runtime.sh

```bash
--arch ARCH              # Architettura: native, arm64, amd64, armv7, 386
--out-dir DIR            # Directory di output (default ./dist/runtime)
--version VERSION        # Versione applicazione
--config FILE            # File di config (MESSO PRIMA dei flag!)
--include-examples       # Includi config.example.json
--include-readme         # Includi README_RUNTIME.md
--add-files PATHS        # File aggiuntivi (separati da :)
--verbose                # Output verbose
```

### deploy_runtime_rsync.sh

```bash
--target TARGET          # Destinazione remota (obbligatorio, nessuna forma posizionale)
--arch ARCH              # Architettura build
--version VERSION        # Versione applicazione
--config FILE            # File di config (prima dei flag!)
--bundle-dir DIR         # Directory temporanea del bundle
--dry-run                # Simulazione senza modifiche (non chiede conferma)
--delete                 # Elimina file remoti non in bundle
--yes, -y                # Salta la conferma di --delete
--rsync-opts OPTS        # Opzioni rsync custom
--exclude LIST           # File mai da eliminare
--verbose                # Output verbose
```

### scripts/start_bot.sh (checkout con sorgenti)

```bash
./scripts/start_bot.sh start     # Avvia bot
./scripts/start_bot.sh stop      # Ferma bot
./scripts/start_bot.sh restart   # Riavvia bot
./scripts/start_bot.sh status    # Mostra stato dettagliato
./scripts/start_bot.sh logs [N]  # Ultimi N log (default: 50)
./scripts/start_bot.sh watchdog  # Riavvia se inattivo (usato da cron)
./scripts/start_bot.sh install   # Installa persistenza e avvia
./scripts/start_bot.sh --dry-run # Anteprima di tutto, senza toccare nulla
```

### start_bot.sh nel bundle (sul NAS)

```bash
./start_bot.sh start          # Avvia bot
./start_bot.sh stop           # Ferma bot
./start_bot.sh restart        # Riavvia bot
./start_bot.sh status         # Stato e PID
./start_bot.sh logs [N]       # Ultimi N log
./start_bot.sh watch          # Log in tempo reale (solo nel bundle!)
./start_bot.sh watchdog       # Riavvia se inattivo
./start_bot.sh config         # Mostra la configurazione risolta
./start_bot.sh config init    # Crea config.json con mode 0600
./start_bot.sh install        # Installa cron e avvia
```

## Variabili d'Ambiente del Bundle

```bash
export NASBOT_LOG_FILE="/var/log/nasbot/app.log"
export NASBOT_PID_FILE="/run/nasbot.pid"
export NASBOT_LOG_MAX_SIZE_MB="50"
export NASBOT_VERBOSE="true"
```

Ogni utente può quindi adattare il sistema alle proprie esigenze specifiche!
