#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd)"
cd "$REPO_ROOT"

required_docs=(
  "docs/BRANCH_PROTECTION.md"
  "docs/GITHUB_RULESET_SETUP.md"
  "docs/CHANGELOG_SETTINGS.md"
  "docs/BOTFATHER_COMMANDS.txt"
)

for f in "${required_docs[@]}"; do
  if [[ ! -f "$f" ]]; then
    echo "quality_check: missing required docs file: $f"
    exit 1
  fi
done

legacy_root_docs=(
  "BRANCH_PROTECTION.md"
  "GITHUB_RULESET_SETUP.md"
  "CHANGELOG_SETTINGS.md"
  "BOTFATHER_COMMANDS.txt"
)

for f in "${legacy_root_docs[@]}"; do
  if [[ -f "$f" ]]; then
    echo "quality_check: legacy root file still present: $f"
    echo "Move it under docs/."
    exit 1
  fi
done

required_scripts=(
  "scripts/build_release.sh"
  "scripts/start_bot.sh"
)

for f in "${required_scripts[@]}"; do
  if [[ ! -f "$f" ]]; then
    echo "quality_check: missing required script: $f"
    exit 1
  fi
done

legacy_root_scripts=(
  "build_release.sh"
  "start_bot.sh"
)

for f in "${legacy_root_scripts[@]}"; do
  if [[ -f "$f" ]]; then
    echo "quality_check: legacy root script still present: $f"
    echo "Move it under scripts/."
    exit 1
  fi
done
# Ensure runtime artifacts are not tracked in git.
for f in nasbot nasbot-arm64 nasbot_state.json config.json; do
  if git ls-files --error-unmatch "$f" >/dev/null 2>&1; then
    echo "quality_check: runtime artifact is tracked by git: $f"
    exit 1
  fi
done

echo "quality_check: passed"
