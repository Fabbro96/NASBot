#!/usr/bin/env bash
set -euo pipefail

# Source common utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
source "${SCRIPT_DIR}/common.sh"

# Configuration variables
DEPLOY_TARGET="${NASBOT_DEPLOY_TARGET:-}"
BUILD_ARCH="${NASBOT_BUILD_ARCH:-native}"
VERSION="${NASBOT_VERSION:-${VERSION:-dev}}"
DRY_RUN="${NASBOT_DRY_RUN:-false}"
DELETE_EXTRA="${NASBOT_DELETE_EXTRA:-false}"
RSYNC_OPTS="${NASBOT_RSYNC_OPTS:-${RSYNC_OPTS:-}}"
BUNDLE_DIR="${NASBOT_BUNDLE_DIR:-dist/runtime-deploy}"
RSYNC_EXCLUDE_DELETE="${NASBOT_RSYNC_EXCLUDE_DELETE:-config.json:nasbot.log:nasbot.pid:nasbot_state.json:#recycle}"

usage() {
	cat <<'EOF'
Usage: ./scripts/deploy_runtime_rsync.sh --target TARGET [OPTIONS]

Build minimal runtime bundle and sync to NAS via rsync.

Required:
  --target TARGET         Remote target: user@host:/path or /local/path
                          Example: fabbro@nas:/Volume1/public

Options:
  --arch ARCH             Build architecture (default: native)
                          Valid: native, arm64, amd64, armv7, 386
  --version VERSION       Version string (default: dev)
  --config FILE           Load config from FILE (default: nasbot.config.local)
  --bundle-dir DIR        Temporary bundle directory (default: dist/runtime-deploy)
  --dry-run               Show what would be synced without copying
  --delete                Delete remote files not in bundle (except protected)
  --rsync-opts OPTS       Additional rsync options
  --exclude LIST          Colon-separated files to never delete (replaces defaults)
  --verbose               Enable verbose output
  --help                  Show this help message

Protected from deletion (even with --delete):
  config.json, nasbot.log, nasbot.pid, nasbot_state.json, #recycle

Environment Variables (override config file):
  NASBOT_DEPLOY_TARGET
  NASBOT_BUILD_ARCH
  NASBOT_VERSION
  NASBOT_DRY_RUN
  NASBOT_DELETE_EXTRA
  NASBOT_RSYNC_OPTS
  NASBOT_BUNDLE_DIR
  NASBOT_RSYNC_EXCLUDE_DELETE
  NASBOT_VERBOSE

Examples:
  # Dry-run to NAS
  ./scripts/deploy_runtime_rsync.sh --target user@nas:/Volume1/public --dry-run
  
  # Deploy ARM64 version with cleanup
  ./scripts/deploy_runtime_rsync.sh --target user@nas:/Volume1/public --arch arm64 --delete
  
  # Deploy to local path
  ./scripts/deploy_runtime_rsync.sh --target /mnt/shares/nasbot-deploy
  
  # Deploy with custom rsync options
  ./scripts/deploy_runtime_rsync.sh --target user@nas:/data --rsync-opts "--bwlimit=1000 --exclude-from=.rsignore"
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--target)
		DEPLOY_TARGET="${2:-}"
		shift 2
		;;
	--arch)
		BUILD_ARCH="${2:-}"
		validate_arch "${BUILD_ARCH}"
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
	--bundle-dir)
		BUNDLE_DIR="${2:-}"
		shift 2
		;;
	--dry-run)
		DRY_RUN="true"
		shift
		;;
	--delete)
		DELETE_EXTRA="true"
		shift
		;;
	--rsync-opts)
		RSYNC_OPTS="${2:-}"
		shift 2
		;;
	--exclude)
		RSYNC_EXCLUDE_DELETE="${2:-}"
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

# Validation
if [[ -z "${DEPLOY_TARGET}" ]]; then
	die "Error: --target is required"
fi

require_cmd rsync
require_cmd go

log_info "NASBot Deployment (rsync-based)"
log_debug "  Target: ${DEPLOY_TARGET}"
log_debug "  Architecture: ${BUILD_ARCH}"
log_debug "  Version: ${VERSION}"
log_debug "  Dry-run: ${DRY_RUN}"
log_debug "  Delete: ${DELETE_EXTRA}"

# Build the runtime bundle
log_info "Building runtime bundle..."
"${SCRIPT_DIR}/package_runtime.sh" \
	--arch "${BUILD_ARCH}" \
	--out-dir "${BUNDLE_DIR}" \
	--version "${VERSION}"

# Prepare rsync arguments
rsync_args=(
	-av
	--human-readable
)

# Add custom rsync options if provided
if [[ -n "${RSYNC_OPTS}" ]]; then
	rsync_args+=(${RSYNC_OPTS})
fi

# Add compression for remote targets
if [[ "${DEPLOY_TARGET}" == *"@"* ]] || [[ "${DEPLOY_TARGET}" == *":"* ]]; then
	rsync_args+=(--compress)
fi

# Add exclusions (never delete these)
IFS=':' read -ra exclude_list <<< "${RSYNC_EXCLUDE_DELETE}"
for item in "${exclude_list[@]}"; do
	[[ -z "${item}" ]] && continue
	rsync_args+=(--exclude "${item}")
	log_debug "  Excluding from deletion: ${item}"
done

# Add delete flag if requested
if [[ "${DELETE_EXTRA}" == "true" ]]; then
	rsync_args+=(--delete)
	log_info "Delete flag enabled (protected files will be preserved)"
fi

# Add dry-run flag if requested
if [[ "${DRY_RUN}" == "true" ]]; then
	rsync_args+=(--dry-run --itemize-changes)
	log_info "DRY-RUN MODE: No files will be modified"
fi

# Execute rsync
log_info "Syncing to: ${DEPLOY_TARGET}"
echo ""
if rsync "${rsync_args[@]}" "${BUNDLE_DIR}/" "${DEPLOY_TARGET}/"; then
	echo ""
	if [[ "${DRY_RUN}" == "true" ]]; then
		log_info "Dry-run completed. Run without --dry-run to apply changes."
	else
		log_info "Deployment completed successfully!"
		log_info "Target: ${DEPLOY_TARGET}"
	fi
else
	die "Deployment failed"
fi
