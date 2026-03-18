#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd)"
cd "$REPO_ROOT"

NAS_TARGET=""
ARCH="native"
VERSION="${VERSION:-dev}"
DRY_RUN="false"
DELETE_EXTRA="false"

usage() {
	cat <<'EOF'
Usage: ./scripts/deploy_runtime_rsync.sh --target user@host:/path [options]

Builds a minimal runtime bundle and syncs it to NAS via rsync.

Required:
  --target        Remote target directory, e.g. user@nas:/Volume1/public

Options:
  --arch          Build architecture: native|arm64 (default: native)
  --version       Version injected with ldflags (default: env VERSION or dev)
  --dry-run       Show what would change without copying
  --delete        Remove extra files on remote except runtime state files
  -h, --help      Show this help

Runtime files synced:
- nasbot
- start_bot.sh
- config.example.json
- README_RUNTIME.txt

Protected from deletion (even with --delete):
- config.json
- nasbot.log
- nasbot.pid
- nasbot_state.json
- #recycle
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--target)
		NAS_TARGET="${2:-}"
		shift 2
		;;
	--arch)
		ARCH="${2:-}"
		shift 2
		;;
	--version)
		VERSION="${2:-}"
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
	-h|--help)
		usage
		exit 0
		;;
	*)
		echo "Unknown option: $1"
		usage
		exit 1
		;;
	esac
done

if [[ -z "$NAS_TARGET" ]]; then
	echo "Error: --target is required"
	usage
	exit 1
fi

if ! command -v rsync >/dev/null 2>&1; then
	echo "Error: rsync not found in PATH"
	exit 1
fi

BUNDLE_DIR="dist/runtime-rsync"

./scripts/package_runtime.sh --arch "$ARCH" --out-dir "$BUNDLE_DIR"

rsync_args=(
	-avz
	--human-readable
	--exclude config.json
	--exclude nasbot.log
	--exclude nasbot.pid
	--exclude nasbot_state.json
	--exclude '#recycle/'
)

if [[ "$DELETE_EXTRA" == "true" ]]; then
	rsync_args+=(--delete)
fi

if [[ "$DRY_RUN" == "true" ]]; then
	rsync_args+=(--dry-run --itemize-changes)
	echo "[DRY-RUN] rsync to $NAS_TARGET"
fi

rsync "${rsync_args[@]}" "$BUNDLE_DIR/" "$NAS_TARGET/"

echo "Deploy completed to: $NAS_TARGET"
