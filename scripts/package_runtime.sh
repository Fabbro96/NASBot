#!/usr/bin/env bash
set -euo pipefail

# Source common utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Override defaults for this script
BUILD_ARCH="${NASBOT_BUILD_ARCH:-native}"
PACKAGE_OUT_DIR="${NASBOT_PACKAGE_OUT_DIR:-./dist/runtime}"
VERSION="${NASBOT_VERSION:-${VERSION:-dev}}"
BINARY_NAME="${NASBOT_BINARY_NAME:-nasbot}"
INCLUDE_EXAMPLE_CONFIG="${NASBOT_INCLUDE_EXAMPLE_CONFIG:-true}"
INCLUDE_README_RUNTIME="${NASBOT_INCLUDE_README_RUNTIME:-true}"
ADDITIONAL_FILES="${NASBOT_ADDITIONAL_FILES:-}"

usage() {
	cat <<'EOF'
Usage: ./scripts/package_runtime.sh [OPTIONS]

Builds a minimal NAS runtime bundle with only essential files.

Options:
  --arch ARCH             Build architecture (default: native)
                          Valid: native, arm64, amd64, armv7, 386
  --out-dir DIR           Output directory (default: ./dist/runtime)
  --version VERSION       Version string (default: dev)
  --config FILE           Load config from FILE (default: nasbot.config.local)
  --include-examples      Include config.example.json (default: true)
  --include-readme        Include README_RUNTIME.txt (default: true)
  --add-files PATHS       Additional files to include (colon-separated paths)
  --verbose               Enable verbose output
  --help                  Show this help message

Environment Variables (override config file):
  NASBOT_BUILD_ARCH
  NASBOT_PACKAGE_OUT_DIR
  NASBOT_VERSION
  NASBOT_BINARY_NAME
  NASBOT_INCLUDE_EXAMPLE_CONFIG
  NASBOT_INCLUDE_README_RUNTIME
  NASBOT_ADDITIONAL_FILES
  NASBOT_VERBOSE

Examples:
  # Build for ARM64
  ./scripts/package_runtime.sh --arch arm64
  
  # Build with custom output directory
  ./scripts/package_runtime.sh --out-dir /tmp/nasbot-bundle
  
  # Build with additional files
  ./scripts/package_runtime.sh --add-files "docs/README.md:LICENSE"
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--arch)
		BUILD_ARCH="${2:-}"
		validate_arch "${BUILD_ARCH}"
		shift 2
		;;
	--out-dir)
		PACKAGE_OUT_DIR="${2:-}"
		shift 2
		;;
	--version)
		VERSION="${2:-}"
		shift 2
		;;
	--config)
		load_config "${2:-}"
		shift 2
		;;
	--include-examples)
		INCLUDE_EXAMPLE_CONFIG="true"
		shift
		;;
	--include-readme)
		INCLUDE_README_RUNTIME="true"
		shift
		;;
	--add-files)
		ADDITIONAL_FILES="${2:-}"
		shift 2
		;;
	--verbose)
		VERBOSE="true"
		shift
		;;
	--help|-h)
		usage
		exit 0
		;;
	*)
		log_error "Unknown option: $1"
		usage
		exit 1
		;;
	esac
done

if [[ -z "$PACKAGE_OUT_DIR" ]]; then
	die "Output directory cannot be empty"
fi

mkdir -p "$PACKAGE_OUT_DIR"

log_info "Packaging NASBot runtime"
log_debug "  Architecture: ${BUILD_ARCH}"
log_debug "  Output: ${PACKAGE_OUT_DIR}"
log_debug "  Version: ${VERSION}"

build_binary() {
	local go_arch
	go_arch=$(get_go_arch "${BUILD_ARCH}")
	local goos goarch
	goos=$(echo "${go_arch}" | cut -d: -f1)
	goarch=$(echo "${go_arch}" | cut -d: -f2)
	
	local binary_path="${PACKAGE_OUT_DIR}/${BINARY_NAME}"
	
	log_info "Building binary: ${binary_path}"
	CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" \
		go build -ldflags "-X main.Version=${VERSION}" \
		-o "${binary_path}" "${REPO_ROOT}"

	# Keep an optional arch-suffixed copy for convenience, but always ship canonical name too.
	if [[ "${BUILD_ARCH}" != "native" ]]; then
		cp "${binary_path}" "${PACKAGE_OUT_DIR}/${BINARY_NAME}-${BUILD_ARCH}"
		chmod +x "${PACKAGE_OUT_DIR}/${BINARY_NAME}-${BUILD_ARCH}"
	fi
	
	chmod +x "${binary_path}"
	log_debug "Binary size: $(get_script_size "${binary_path}")"
}

build_binary

# Copy runtime manager script
log_info "Copying runtime manager scripts"
cp scripts/start_bot_runtime.sh "${PACKAGE_OUT_DIR}/start_bot.sh"
chmod +x "${PACKAGE_OUT_DIR}/start_bot.sh"

# Copy config example if requested
if [[ "${INCLUDE_EXAMPLE_CONFIG}" == "true" ]] && [[ -f "config.example.json" ]]; then
	log_debug "Copying config.example.json"
	cp config.example.json "${PACKAGE_OUT_DIR}/config.example.json"
fi

# Copy config utilities if present
if [[ -f "nasbot.config.template" ]]; then
	log_debug "Copying nasbot.config.template"
	cp nasbot.config.template "${PACKAGE_OUT_DIR}/nasbot.config.template"
fi

# Add additional files if specified
if [[ -n "${ADDITIONAL_FILES}" ]]; then
	log_info "Including additional files"
	IFS=':' read -ra files <<< "${ADDITIONAL_FILES}"
	for file in "${files[@]}"; do
		if [[ -f "${file}" ]]; then
			log_debug "  Adding: ${file}"
			cp "${file}" "${PACKAGE_OUT_DIR}/"
		else
			log_warn "  File not found: ${file}"
		fi
	done
fi

# Create README if requested
if [[ "${INCLUDE_README_RUNTIME}" == "true" ]]; then
	log_debug "Creating README_RUNTIME.txt"
	cat >"${PACKAGE_OUT_DIR}/README_RUNTIME.txt" <<'READMEOF'
NASBot Runtime Bundle (Minimal - No Source Code)

## Quick Start

1. Copy config.example.json to config.json:
   cp config.example.json config.json

2. Edit config.json with your settings:
   - bot_token: Your Telegram bot token
   - allowed_user_id: Your Telegram user ID
   - Other settings as needed

3. Start the bot:
   ./start_bot.sh start

## Supported Commands

./start_bot.sh start       - Start the bot
./start_bot.sh stop        - Stop the bot gracefully
./start_bot.sh restart     - Restart the bot
./start_bot.sh status      - Show bot status and PID
./start_bot.sh logs        - Show recent logs
./start_bot.sh watch       - Watch logs in real-time

## Runtime Files

Generated automatically:
- nasbot.log          - Application logs
- nasbot.pid          - Process ID file
- nasbot_state.json   - Bot state backup

## Updates

To update the bot:
1. Place new binary as: nasbot-update (or nasbot-update-arm64 for ARM)
2. Run: ./start_bot.sh restart
3. The script will detect the update and restart with new version

## Configuration via Environment Variables

Override default paths using environment variables:
  NASBOT_LOG_FILE        - Path to log file
  NASBOT_PID_FILE        - Path to PID file
  NASBOT_STATE_FILE      - Path to state file

Example:
  export NASBOT_LOG_FILE=/var/log/nasbot.log
  export NASBOT_PID_FILE=/var/run/nasbot.pid
  ./start_bot.sh start

## Troubleshooting

### Bot won't start
- Check config.json exists and is valid JSON
- Check bot_token is correct
- View logs: ./start_bot.sh logs

### Permission errors
- Ensure start_bot.sh is executable: chmod +x start_bot.sh
- Ensure nasbot binary is executable: chmod +x nasbot

### Can't find binary
- Ensure nasbot binary is in the same directory as start_bot.sh
- Check file permissions: ls -la nasbot

READMEOF
fi

log_info "Runtime bundle created successfully"
log_info "Location: ${PACKAGE_OUT_DIR}"
echo ""
ls -lh "${PACKAGE_OUT_DIR}"
