#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd)"
cd "$REPO_ROOT"

if ! command -v shellcheck >/dev/null 2>&1; then
  echo "❌ shellcheck is required"
  echo "   Ubuntu/Debian: sudo apt-get update && sudo apt-get install -y shellcheck"
  exit 1
fi

mapfile -t shell_files < <(find . -type f -name "*.sh" -not -path "./.git/*" | sort)

if [[ ${#shell_files[@]} -eq 0 ]]; then
  echo "No shell scripts found."
  exit 0
fi

shellcheck \
  --severity=warning \
  --shell=bash \
  "${shell_files[@]}"

echo "shellcheck: all scripts passed"
