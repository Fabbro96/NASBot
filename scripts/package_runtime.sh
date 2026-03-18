#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd)"
cd "$REPO_ROOT"

ARCH="native"
OUT_DIR="dist/runtime"
VERSION="${VERSION:-dev}"

usage() {
	cat <<'EOF'
Usage: ./scripts/package_runtime.sh [--arch native|arm64] [--out-dir PATH]

Builds a minimal NAS runtime bundle with only essential files:
- nasbot
- start_bot.sh
- config.example.json

Options:
  --arch      Build architecture (default: native)
  --out-dir   Output directory (default: dist/runtime)
  -h, --help  Show help
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--arch)
		ARCH="${2:-}"
		shift 2
		;;
	--out-dir)
		OUT_DIR="${2:-}"
		shift 2
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

if [[ -z "$OUT_DIR" ]]; then
	echo "Output directory cannot be empty"
	exit 1
fi

mkdir -p "$OUT_DIR"

build_binary() {
	case "$ARCH" in
	native)
		go build -ldflags "-X main.Version=${VERSION}" -o "$OUT_DIR/nasbot" .
		;;
	arm64)
		CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=${VERSION}" -o "$OUT_DIR/nasbot" .
		;;
	*)
		echo "Unsupported --arch '$ARCH' (use native or arm64)"
		exit 1
		;;
	esac
}

build_binary
cp scripts/start_bot_runtime.sh "$OUT_DIR/start_bot.sh"
chmod +x "$OUT_DIR/start_bot.sh" "$OUT_DIR/nasbot"
cp config.example.json "$OUT_DIR/config.example.json"

cat >"$OUT_DIR/README_RUNTIME.txt" <<'EOF'
NASBot runtime bundle (minimal)

Keep only these files in your NAS folder:
- nasbot
- start_bot.sh
- config.json (create from config.example.json)
- runtime-generated: nasbot.log, nasbot.pid, nasbot_state.json

Quick start:
1) cp config.example.json config.json
2) Edit config.json (bot_token, allowed_user_id, etc)
3) ./start_bot.sh start

Update flow:
- Drop new binary as nasbot-update (or nasbot-update-arm64/amd64)
- Run ./start_bot.sh restart
EOF

echo "Runtime bundle created in: $OUT_DIR"
ls -lh "$OUT_DIR"
