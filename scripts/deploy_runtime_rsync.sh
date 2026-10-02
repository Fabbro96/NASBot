#!/usr/bin/env bash
set -euo pipefail

# Source common utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/common.sh"

# Configuration variables
DEPLOY_TARGET="${NASBOT_DEPLOY_TARGET:-}"
BUILD_ARCH="${NASBOT_BUILD_ARCH:-native}"
VERSION="${NASBOT_VERSION:-${VERSION:-dev}}"
DRY_RUN="${NASBOT_DRY_RUN:-false}"
DELETE_EXTRA="${NASBOT_DELETE_EXTRA:-false}"
ASSUME_YES="${NASBOT_ASSUME_YES:-false}"
RSYNC_OPTS="${NASBOT_RSYNC_OPTS:-${RSYNC_OPTS:-}}"
BUNDLE_DIR="${NASBOT_BUNDLE_DIR:-dist/runtime-deploy}"
RSYNC_EXCLUDE_DELETE="${NASBOT_RSYNC_EXCLUDE_DELETE:-config.json:nasbot.log:nasbot.pid:nasbot_state.json:#recycle}"

# Percorsi che non devono MAI ricevere --delete: sono root di sistema o home di
# utenti, dove cancellare ciò che non è nel bundle significa cancellare dati.
# Un target con un solo segmento ("/Volume1", "/mnt", "/data") è già respinto
# dalla regola dei due segmenti; questa lista copre i casi espliciti.
FORBIDDEN_TARGET_PATHS=(
	"/"
	"/home"
	"/usr"
	"/etc"
	"/var"
	"/tmp"
	"/root"
	"/opt"
	"/srv"
	"/boot"
)

# Estrae il solo percorso dal target, tollerando le due forme supportate:
#   /local/path      -> /local/path
#   user@host:/path  -> /path
target_path() {
	local target="$1"
	case "$target" in
	*:*)
		printf '%s\n' "${target#*:}"
		;;
	*)
		printf '%s\n' "$target"
		;;
	esac
}

# Conta i segmenti significativi di un percorso: "/Volume1/public" -> 2,
# "/" -> 0, "/mnt" -> 1, "/mnt//nasbot/" -> 2.
count_path_segments() {
	local path="${1#/}"
	local count=0
	local segment
	local old_ifs="$IFS"
	IFS='/'
	# Lo split su "/" è intenzionale: IFS è impostato poco sopra.
	# shellcheck disable=SC2086
	for segment in $path; do
		[[ -n "$segment" && "$segment" != "." && "$segment" != ".." ]] && count=$((count + 1))
	done
	IFS="$old_ifs"
	printf '%s\n' "$count"
}

# Rifiuta i target troppo larghi PRIMA di costruire il bundle: --delete verso
# una destinazione non validata cancella tutto ciò che non è nel bundle.
validate_deploy_target() {
	local target="$1"
	local path
	path=$(target_path "$target")

	if [[ "$path" != /* ]]; then
		die "Error: --target '${target}' must contain an absolute path (got '${path}'). Example: fabbro@nas:/Volume1/public"
	fi

	local forbidden
	for forbidden in "${FORBIDDEN_TARGET_PATHS[@]}"; do
		if [[ "$path" == "$forbidden" ]]; then
			die "Error: --target '${target}' points to the protected location '${path}'. Deploy into a dedicated subdirectory instead."
		fi
	done

	if [[ "$path" == */ ]]; then
		die "Error: --target '${target}' must not end with '/'. Point to the exact bundle directory, for example '${target%/}'."
	fi

	local segments
	segments=$(count_path_segments "$path")
	if [[ "${segments}" -lt 2 ]]; then
		die "Error: --target '${target}' is too broad: '${path}' has ${segments} path segment(s), at least 2 are required. Use a dedicated subdirectory, for example '${path%/}/nasbot'."
	fi
}

# --delete è distruttivo: verso un percorso assoluto chiede conferma esplicita.
# Fuori da un terminale (CI, cron) il default è rifiutare: --yes è l'unico
# modo per procedere senza interazione.
confirm_delete_absolute() {
	local target="$1"

	if [[ "$ASSUME_YES" == "true" ]]; then
		log_warn "  Confirmation skipped (--yes): proceeding with --delete."
		return 0
	fi
	if [[ ! -t 0 ]]; then
		log_error "Refusing to run --delete towards the absolute path '${target}' without a terminal."
		log_error "Re-run with --yes if this is really what you want."
		return 1
	fi

	local reply=""
	echo ""
	log_warn "  --delete will remove from '${target}' every file that is not part of the bundle."
	log_warn "  Only these names are protected: ${RSYNC_EXCLUDE_DELETE//:/, }"
	read -r -p "  Proceed? [y/N] " reply || true
	[[ "$reply" =~ ^[Yy]([Ee][Ss])?$ ]]
}

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
  --dry-run               Show what would be synced (and deleted) without copying.
                          With --delete it needs no confirmation.
  --delete                Delete remote files not in bundle (except protected).
                          With an absolute path, asks for confirmation.
  --yes, -y               Skip the confirmation prompt of --delete
  --rsync-opts OPTS       Additional rsync options
  --exclude LIST          Colon-separated files to never delete (replaces defaults)
  --verbose               Enable verbose output
  --help                  Show this help message

Target validation (refused before anything is built or synced):
  * the path must be absolute
  * the path must not end with '/'
  * the path must have at least 2 segments (no '/Volume1', no '/mnt')
  * the path must not be /, /home, /usr, /etc, /var, /tmp, /root, /opt,
    /srv or /boot

Protected from deletion (even with --delete):
  config.json, nasbot.log, nasbot.pid, nasbot_state.json, #recycle

Environment Variables (override config file):
  NASBOT_DEPLOY_TARGET
  NASBOT_BUILD_ARCH
  NASBOT_VERSION
  NASBOT_DRY_RUN
  NASBOT_DELETE_EXTRA
  NASBOT_ASSUME_YES
  NASBOT_RSYNC_OPTS
  NASBOT_BUNDLE_DIR
  NASBOT_RSYNC_EXCLUDE_DELETE
  NASBOT_VERBOSE

Examples:
  # Dry-run to NAS
  ./scripts/deploy_runtime_rsync.sh --target user@nas:/Volume1/public --dry-run

  # Deploy ARM64 version with cleanup (asks for confirmation)
  ./scripts/deploy_runtime_rsync.sh --target user@nas:/Volume1/public --arch arm64 --delete

  # Non-interactive deploy with cleanup (skips the prompt)
  ./scripts/deploy_runtime_rsync.sh --target user@nas:/Volume1/public --delete --yes

  # Deploy to local path
  ./scripts/deploy_runtime_rsync.sh --target /mnt/shares/nasbot-deploy

  # Deploy with custom rsync options
  ./scripts/deploy_runtime_rsync.sh --target user@nas:/Volume1/nasbot --rsync-opts "--bwlimit=1000 --exclude-from=.rsignore"
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
	--yes | -y)
		ASSUME_YES="true"
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
		export VERBOSE="true"
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

# Structural validation first, so an obviously wrong target is refused with the
# reason (trailing slash, too few segments, protected location) rather than
# with the generic "no terminal" message of the confirmation below.
validate_deploy_target "${DEPLOY_TARGET}"

# --delete is destructive: with an absolute path require an explicit
# confirmation (or --yes) BEFORE building the bundle or touching rsync.
# A dry-run never deletes anything, so it stays usable as the preview of a
# destructive deploy without a prompt: that is the point of --dry-run.
if [[ "${DELETE_EXTRA}" == "true" && "${DRY_RUN}" != "true" ]] && [[ "$(target_path "${DEPLOY_TARGET}")" == /* ]]; then
	confirm_delete_absolute "$(target_path "${DEPLOY_TARGET}")" || die "Aborted: --delete not confirmed."
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
	read -r -a rsync_opts_arr <<< "${RSYNC_OPTS}"
	rsync_args+=("${rsync_opts_arr[@]}")
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
	# The target has already been validated and confirmed above.
	rsync_args+=(--delete)
	log_info "Delete flag enabled (protected files will be preserved)"
fi

# Add dry-run flag if requested. rsync's --dry-run is real output, not a stub:
# it prints the itemized list of transfers and deletions it would perform.
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
