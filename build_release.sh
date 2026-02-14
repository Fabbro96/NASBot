#!/usr/bin/env bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
cd "$SCRIPT_DIR"

clean="true"

usage() {
    cat <<'EOF'
Usage: ./build_release.sh [--no-clean]

Options:
  --no-clean   Keep existing binaries before build
  -h, --help   Show this help message
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
    --no-clean)
        clean="false"
        shift
        ;;
    -h|--help)
        usage
        exit 0
        ;;
    *)
        echo -e "${RED}Unknown argument: $1${NC}"
        usage
        exit 1
        ;;
    esac
done

if ! command -v go >/dev/null 2>&1; then
    echo -e "${RED}❌ Go not found in PATH${NC}"
    exit 1
fi

echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}  🛠  NASBot Build Script${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"

if [[ "$clean" == "true" ]]; then
    rm -f nasbot nasbot-arm64
fi

echo -e "${YELLOW}Building for current architecture...${NC}"
go build -o nasbot .
echo -e "${GREEN}✅ Success: nasbot${NC}"

echo -e "${YELLOW}Building for generic ARM64 (Linux)...${NC}"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o nasbot-arm64 .
echo -e "${GREEN}✅ Success: nasbot-arm64${NC}"

echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}🎉 Build complete!${NC}"
ls -lh nasbot nasbot-arm64
