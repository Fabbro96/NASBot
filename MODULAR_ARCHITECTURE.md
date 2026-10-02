# NASBot Modular Architecture

## System Overview

```
┌─────────────────────────────────────────────────────────┐
│          NASBot Modular Deployment System              │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Configuration Layer (Hierarchical Priority)            │
│  ────────────────────────────────────────────────────   │
│  Resolved by load_config(), in this order:              │
│  1. Script Defaults: DEFAULTS array in common.sh       │
│  2. Default Template: nasbot.config.template            │
│  3. Local Config: nasbot.config.local                   │
│  4. Environment Variables: NASBOT_*                     │
│                                                         │
│  Command-line flags are parsed separately and are       │
│  NOT the top of the hierarchy: --config re-runs the     │
│  whole load at parse time, so it overwrites the flags   │
│  that came before it. See MODULAR_CONFIG_GUIDE.md.      │
│                                                         │
│  Core Scripts (All use common.sh)                       │
│  ───────────────────────────────────                    │
│                                                         │
│  ┌──────────────────────────────────────────────────┐  │
│  │ scripts/common.sh (Foundation)                  │  │
│  │ ────────────────────────────────────────────    │  │
│  │ • load_config()                                 │  │
│  │ • resolve_path()                                │  │
│  │ • log functions (info, warn, error)            │  │
│  │ • validate_arch()                               │  │
│  │ • get_go_arch() / get_arch_suffix()            │  │
│  │ • require_dir() / require_file() / require_cmd()│  │
│  │ • is_process_running() / read_pid()            │  │
│  │ • format_bytes() / get_script_size()           │  │
│  │ • safe_copy() / find_config()                  │  │
│  │ • print_config() / export_config()             │  │
│  └──────────────────────────────────────────────────┘  │
│               ▲ sourced by all scripts                 │
│              /|\                                        │
│             / | \\                                      │
│            /  |  \\                                     │
│           /   |   \\                                    │
│    ┌─────────┴─────────────────────────────────┐       │
│    │                                           │       │
│  ┌──────────────────┐  ┌──────────────────┐  ┌───────────────────┐
│  │ package_runtime  │  │ deploy_runtime   │  │ start_bot_runtime │
│  │     .sh          │  │    _rsync.sh     │  │       .sh         │
│  │ ────────────────│  │ ─────────────────│  │ ──────────────────│
│  │ • Build binary  │  │ • Call package   │  │ • Start/stop bot  │
│  │ • Copy files    │  │ • rsync bundles  │  │ • Log rotation    │
│  │ • Create dist   │  │ • Dry-run mode   │  │ • Auto-restart    │
│  │ • Custom files  │  │ • Protect state  │  │ • Status monitor  │
│  └──────────────────┘  └──────────────────┘  │ • Watch logs      │
│                                              │ • Config init     │
│                                              └───────────────────┘
│                                                         │
│  Configuration Templates                                │
│  ──────────────────────────────────────────────────    │
│  • nasbot.config.template (defaults + docs)            │
│  • nasbot.config.example (real-world scenarios)        │
│  • nasbot.config.local (user-specific)                 │
│                                                         │
└─────────────────────────────────────────────────────────┘
```

## File Structure

```
.
├── scripts/
│   ├── common.sh                    # Shared utilities & config loading (sourced by all)
│   ├── package_runtime.sh           # Build minimal deployment bundle
│   ├── deploy_runtime_rsync.sh      # Validate target, build bundle, sync via rsync
│   ├── start_bot.sh                 # Runner for a source checkout (bin/ + var/)
│   └── start_bot_runtime.sh         # Runner shipped inside the bundle (start/stop/logs/watch/config)
│
├── nasbot.config.template           # Default configuration file (documented)
├── nasbot.config.example            # Real-world configuration examples
└── nasbot.config.local              # Local customization (gitignored)
    └── [user creates this by copying one of above]
```

`nasbot.config.example` is **not** loaded by anything: `load_config` reads
`nasbot.config.template` from the repository root plus the file you pass to `--config`.
Keep the example as documentation, or copy it by hand.

## Configuration Loading Flow

```
Script execution: ./scripts/deploy_runtime_rsync.sh --config nasbot.config.local --target user@nas:/path

┌─────────────────────────────────────────┐
│ 1. Script sources scripts/common.sh     │
│    └─ Defines DEFAULTS array            │
│    └─ Defines load_config() function    │
│    └─ Auto-calls load_config()          │
│       (skipped if NASBOT_NO_AUTO_LOAD=1)│
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│ 2. load_config() runs, applying in order│
│                                          │
│    Step A: Apply DEFAULTS values        │
│    Step B: Source nasbot.config.template│
│    Step C: Source nasbot.config.local   │
│    Step D: Override with NASBOT_* envs  │
│           (only for keys in DEFAULTS)   │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│ 3. Script re-initialises its own        │
│    variables from the environment       │
│    e.g. BUILD_ARCH="${NASBOT_BUILD_     │
│              ARCH:-native}"             │
│    → values from step 2 are DISCARDED   │
│      for these variables                │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│ 4. Parse command-line arguments, left   │
│    to right. --config calls load_config │
│    AGAIN, so it overwrites every flag   │
│    parsed before it.                    │
│    └─ --config nasbot.config.local      │
│    └─ --target user@nas:/path           │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│ 5. Final values available for script    │
│    BUILD_ARCH="arm64"                   │
│    DEPLOY_TARGET="user@nas:/path"       │
│    (plus all other vars from config)    │
└─────────────────────────────────────────┘
```

Because of steps 3 and 4, **argument order is part of the semantics**: put `--config`
before the flags you want to keep.

## Priority Resolution Example

`BUILD_ARCH`, with `cfg.conf` containing `BUILD_ARCH="amd64"`:

```
When the value is decided        Source                          Value
──────────────────────────       ──────────                      ─────
last                            flag AFTER --config             --arch arm64        arm64
                                NASBOT_BUILD_ARCH               NASBOT_BUILD_ARCH=amd64   amd64
                                --config FILE                   cfg.conf             amd64
re-run of                        nasbot.config.local            (same key)          amd64
load_config                      nasbot.config.template         BUILD_ARCH="native" native
first                            DEFAULTS array in common.sh    [BUILD_ARCH]        native
                                script re-init (step 3)         "${NASBOT_BUILD_ARCH:-native}"
```

The last line is the important one: for the variables a script re-initialises from the
environment (`BUILD_ARCH`, `PACKAGE_OUT_DIR`, `VERSION`, `BINARY_NAME`,
`INCLUDE_EXAMPLE_CONFIG`, `INCLUDE_README_RUNTIME`, `ADDITIONAL_FILES` in
`package_runtime.sh`), the auto-loaded config file is thrown away before any flag is
parsed, and only a later `--config` puts it back.

Verified by running the script and inspecting the name of the arch-suffixed binary in
the resulting bundle:

```
package_runtime.sh --arch arm64 --config cfg.conf
  -> nasbot-amd64   the config file beat the earlier flag
package_runtime.sh --config cfg.conf --arch arm64
  -> nasbot-arm64   the later flag beat the config file
```

Keys the scripts read **later** rather than at re-initialisation (`DEPLOY_TARGET`,
`RSYNC_OPTS`, `RSYNC_EXCLUDE_DELETE`, `ADDITIONAL_FILES`) do survive a `--config`
reload, because the reload happens before those reads. See MODULAR_CONFIG_GUIDE.md.

## Modularity Benefits

### For Individual Users
- Customize settings without editing scripts
- Multiple configs for different environments
- Easy to understand and modify
- Can include additional files (docs, scripts, configs)

### For Organization
- Standardize build process across teams
- Override defaults with org policies via env vars
- Easy CI/CD integration (GitHub Actions, GitLab CI, etc.)
- Version-controlled configurations

### For Deployment
- One-command deployments: `./scripts/deploy_runtime_rsync.sh --target user@nas:/path`
  (`--target` is required; there is no positional form)
- Dry-run testing before actual deployment
- Automatic file protection (never delete state files)
- Support for multiple NAS targets with different configs

## Usage Patterns

### Pattern 1: One-Off Command

```bash
# No config needed - uses all defaults
./scripts/package_runtime.sh

# Override specific setting
NASBOT_BUILD_ARCH=arm64 ./scripts/package_runtime.sh
```

### Pattern 2: Local Config

```bash
# Create local config
cp nasbot.config.template nasbot.config.local
nano nasbot.config.local

# Use it consistently. Keep --config FIRST: it re-loads the hierarchy at parse
# time, so anything you pass after it still has the last word.
./scripts/package_runtime.sh --config nasbot.config.local
./scripts/deploy_runtime_rsync.sh --config nasbot.config.local
```

### Pattern 3: Multi-Environment

```bash
# Create environment-specific configs
cp nasbot.config.template nasbot.config.dev
cp nasbot.config.template nasbot.config.prod

# Use selectively
./scripts/package_runtime.sh --config nasbot.config.dev
./scripts/deploy_runtime_rsync.sh --config nasbot.config.prod --delete
```

### Pattern 4: CI/CD Pipeline

```yaml
# GitHub Actions example
- name: Build NASBot
  env:
    NASBOT_BUILD_ARCH: arm64
    NASBOT_VERSION: ${{ github.ref }}
    NASBOT_VERBOSE: true
  run: |
    ./scripts/package_runtime.sh
    ./scripts/deploy_runtime_rsync.sh --target ${{ secrets.NAS_TARGET }}
```

### Pattern 5: Custom Wrapper Script

```bash
#!/bin/bash
# deploy-to-prod.sh - wrapper with organization defaults
set -euo pipefail

export NASBOT_BUILD_ARCH="${1:-arm64}"
export NASBOT_DEPLOY_TARGET="${PROD_NAS_TARGET}"
export NASBOT_DELETE_EXTRA="true"
export NASBOT_VERSION="v$(date +%Y%m%d)"

./scripts/package_runtime.sh

# --target is required and has no positional form. The env var alone is enough,
# but passing it explicitly documents the intent.
./scripts/deploy_runtime_rsync.sh --target "${NASBOT_DEPLOY_TARGET}" --dry-run
read -r -p "Continue with deployment? (y/n) " reply
[[ "$reply" =~ ^[Yy]$ ]] && \
  ./scripts/deploy_runtime_rsync.sh --target "${NASBOT_DEPLOY_TARGET}" --yes
```

`--yes` is what makes the second command work in a non-interactive context: `--delete`
towards an absolute path otherwise refuses to run without a terminal.

## Extensibility Examples

### Add Custom Features to Your Config

```bash
# nasbot.config.local with custom app settings

# Standard NASBot settings
BUILD_ARCH="arm64"
DEPLOY_TARGET="user@nas:/data"

# Custom application settings
# (These won't interfere with NASBot script settings)
CUSTOM_DATADIR="/Volume1/data"
CUSTOM_WEBHOOKS="enabled"
CUSTOM_TIMEOUT="30s"
CUSTOM_RETRY_COUNT="3"
```

Custom keys are inert until a script of yours reads them. They cannot be read through
`NASBOT_CUSTOM_*` either: `load_config` only applies environment overrides for the keys
listed in its `DEFAULTS` array.

### Custom Deployment Script Using common.sh

```bash
#!/bin/bash
set -euo pipefail
source scripts/common.sh

# Load all configuration
load_config "nasbot.config.local"

# Use provided helpers
log_info "Starting custom deployment..."
require_cmd rsync
require_cmd go
require_dir "/opt/nasbot"    # the target must exist and be writable locally

# Access configured values
log_info "Deploying to: ${DEPLOY_TARGET}"
log_info "Version: ${VERSION}"

# Build your custom logic
./scripts/package_runtime.sh --config nasbot.config.local
# ... custom rsync logic ...
```

> `DEPLOY_PATH` is one of the keys nothing reads (see MODULAR_CONFIG_GUIDE.md): use a
> literal path or your own variable instead of expecting it to be pre-filled.

## Testing Your Configuration

```bash
# 1. Validate syntax
bash -n nasbot.config.local

# 2. Verify values
grep -E '^\s*[A-Z_]+=' nasbot.config.local

# 3. Test with verbose output (the [DEBUG] lines show every resolved key)
./scripts/package_runtime.sh --verbose --config nasbot.config.local

# 4. Dry-run deployment
./scripts/deploy_runtime_rsync.sh --config nasbot.config.local --dry-run

# 5. Inspect the bundle the runner will use (bundle only, on the NAS)
NASBOT_LOG_FILE=/var/log/nasbot/app.log ./start_bot.sh config
```

## Migration Guide

### From Static Scripts to Modular

**Before:**
```bash
# Edit script directly
vim scripts/package_runtime.sh
# Change: BUILD_ARCH="${NASBOT_BUILD_ARCH:-native}" → "arm64"
```

**After:**
```bash
# Create config
cat > nasbot.config.local <<EOF
BUILD_ARCH="arm64"
DEPLOY_TARGET="user@nas:/path/nasbot"
EOF

# Use it --config first, flags after
./scripts/package_runtime.sh --config nasbot.config.local
```

## Performance Considerations

- Configuration loading is fast (simple sourcing)
- No dynamic lookups or network calls
- Environment variables override (no I/O needed)
- Common.sh functions are lightweight utilities

## Security Notes

- These config files belong to the **build and deploy scripts**, not to the bot. They
  must never contain a `bot_token`: the bot's own secrets live in `config.json`, which
  is read by the Go binary and must stay `chmod 600`.
- Config files can contain sensitive paths, so keep them private anyway:
  `chmod 600 nasbot.config.local`
- Don't commit local configs to git (they are gitignored).
- Use `--dry-run` before destructive operations like `--delete`.
- `--delete` refuses to run towards a broad path (`/`, `/home`, `/usr`, …) or with fewer
  than two segments, and needs `--yes` in a non-interactive context.

## Summary

The modular system provides:

✓ **Flexibility** - Each user customizes for their needs
✓ **Reusability** - One set of scripts, many configurations
✓ **Maintainability** - Scripts don't change, configs do
✓ **Safety** - Dry-run before real deployments
✓ **Scalability** - From single NAS to multi-environment setups
✓ **Clarity** - Configuration-as-code philosophy, with the resolution order documented
✓ **Integration** - Works with CI/CD, containers, automation
