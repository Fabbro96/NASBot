# NASBot Modular Architecture

## System Overview

```
┌─────────────────────────────────────────────────────────┐
│          NASBot Modular Deployment System              │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  Configuration Layer (Hierarchical Priority)            │
│  ────────────────────────────────────────────────────   │
│  1. Environment Variables: NASBOT_*                     │
│  2. Command-Line Flags: --arch, --out-dir, etc         │
│  3. Local Config: nasbot.config.local                   │
│  4. Default Template: nasbot.config.template            │
│  5. Script Defaults: (hardcoded in scripts)             │
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
│   ├── common.sh                    # Shared utilities & config loading
│   ├── package_runtime.sh           # Build minimal deployment bundle
│   ├── deploy_runtime_rsync.sh      # Sync bundle to NAS via rsync
│   └── start_bot_runtime.sh         # Runtime manager (start/stop/logs/etc)
│
├── nasbot.config.template           # Default configuration file (documented)
├── nasbot.config.example            # Real-world configuration examples
└── nasbot.config.local              # Local customization (gitignored)
    └── [user creates this by copying one of above]
```

## Configuration Loading Flow

```
Script execution: ./scripts/deploy_runtime_rsync.sh --arch arm64 --target user@nas:/path

┌─────────────────────────────────────────┐
│ 1. Script sources scripts/common.sh     │
│    └─ Defines DEFAULTS array            │
│    └─ Defines load_config() function    │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│ 2. load_config() called automatically    │
│    (if NASBOT_NO_AUTO_LOAD != 1)        │
│                                          │
│    Step A: Apply DEFAULTS values        │
│    Step B: Source nasbot.config.template│
│    Step C: Source nasbot.config.local   │
│    Step D: Override with NASBOT_* envs  │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│ 3. Parse command-line arguments         │
│    (overrides config values)            │
│    └─ --arch arm64                      │
│    └─ --target user@nas:/path           │
└─────────────────┬───────────────────────┘
                  │
┌─────────────────▼───────────────────────┐
│ 4. Final values available for script    │
│    BUILD_ARCH="arm64"                   │
│    DEPLOY_TARGET="user@nas:/path"       │
│    (plus all other vars from config)    │
└─────────────────────────────────────────┘
```

## Priority Resolution Example

```
Setting: LOG_FILE

Priority                Source                      Value
───────────             ──────────                  ─────
1 (Highest)   Command-line flag                    [none for LOG_FILE]
2             Env variable: NASBOT_LOG_FILE         /custom/logs/app.log ✓ USED
3             nasbot.config.local:                  /var/log/nasbot.log
              LOG_FILE="/var/log/nasbot.log"
4             nasbot.config.template default:       nasbot.log
              LOG_FILE="nasbot.log"
5 (Lowest)    Hard-coded default in common.sh      (none)

RESULT: LOG_FILE="/custom/logs/app.log"
        (From environment variable override)
```

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

# Use it consistently
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
source scripts/common.sh

export NASBOT_BUILD_ARCH="${1:-arm64}"
export NASBOT_DEPLOY_TARGET="${PROD_NAS_TARGET}"
export NASBOT_DELETE_EXTRA="true"
export NASBOT_VERSION="v$(date +%Y%m%d)"

./scripts/package_runtime.sh
./scripts/deploy_runtime_rsync.sh --dry-run
read -p "Continue with deployment? (y/n) " -n 1 -r; echo
[[ "$REPLY" =~ ^[Yy]$ ]] && ./scripts/deploy_runtime_rsync.sh
```

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

### Custom Deployment Script Using common.sh

```bash
#!/bin/bash
source scripts/common.sh

# Load all configuration
load_config "nasbot.config.local"

# Use provided helpers
log_info "Starting custom deployment..."
require_cmd rsync
require_dir "${DEPLOY_PATH}"

# Access configured values
log_info "Deploying to: ${DEPLOY_TARGET}"
log_info "Version: ${VERSION}"

# Build your custom logic
./scripts/package_runtime.sh --config nasbot.config.local
# ... custom rsync logic ...
```

## Testing Your Configuration

```bash
# 1. Validate syntax
bash -n nasbot.config.local

# 2. Verify values
grep -E '^\s*[A-Z_]+=' nasbot.config.local

# 3. Test with verbose output
NASBOT_VERBOSE=true ./scripts/package_runtime.sh --config nasbot.config.local

# 4. Dry-run deployment
./scripts/deploy_runtime_rsync.sh --config nasbot.config.local --dry-run

# 5. Check bot configuration
NASBOT_LOG_FILE=/some/path ./start_bot.sh config
```

## Migration Guide

### From Static Scripts to Modular

**Before:**
```bash
# Edit script directly
vim scripts/package_runtime.sh
# Change: ARCH="native" → ARCH="arm64"
```

**After:**
```bash
# Create config
cat > nasbot.config.local <<EOF
BUILD_ARCH="arm64"
DEPLOY_TARGET="user@nas:/path"
EOF

# Use it
./scripts/package_runtime.sh --config nasbot.config.local
```

## Performance Considerations

- Configuration loading is fast (simple sourcing)
- No dynamic lookups or network calls
- Environment variables override (no I/O needed)
- Common.sh functions are lightweight utilities

## Security Notes

- Config files can contain sensitive paths but not passwords
- Store actual secrets in environment variables or `.env`
- Use restricted file permissions: `chmod 600 nasbot.config.local`
- Don't commit local configs to git (use .gitignore)
- Use `--dry-run` before destructive operations like `--delete`

## Summary

The modular system provides:

✓ **Flexibility** - Each user customizes for their needs
✓ **Reusability** - One set of scripts, infinite configurations  
✓ **Maintainability** - Scripts don't change, configs do
✓ **Safety** - Dry-run before real deployments
✓ **Scalability** - From single NAS to multi-environment setups
✓ **Clarity** - Configuration-as-code philosophy
✓ **Integration** - Works with CI/CD, containers, automation
