#!/usr/bin/env bash
set -euo pipefail

mode="staged"
if [[ "${1:-}" == "--repo" ]]; then
  mode="repo"
fi

patterns=(
  '"bot_token"\s*:\s*"[^"]{10,}"'
  '"gemini_api_key"\s*:\s*"[^"]{10,}"'
  '[0-9]{8,}:[A-Za-z0-9_-]{25,}'
  'AIza[0-9A-Za-z\-_]{20,}'
)

if [[ "$mode" == "staged" ]]; then
  staged_files=$(git diff --cached --name-only --diff-filter=ACM)
  if [[ -z "${staged_files}" ]]; then
    exit 0
  fi

  staged_content=$(git diff --cached | cat)
  for pattern in "${patterns[@]}"; do
    if printf "%s" "$staged_content" | grep -E -q "$pattern"; then
      echo "❌ Secret-like content detected in staged changes."
      echo "   Pattern: $pattern"
      echo "   Remove secrets before committing."
      exit 1
    fi
  done
  exit 0
fi

tracked_files=$(git ls-files)
if [[ -z "${tracked_files}" ]]; then
  exit 0
fi

for pattern in "${patterns[@]}"; do
  if git grep -nE "$pattern" -- \
  ':!*_test.go' \
      ':!config.example.json' \
      ':!CHANGELOG_SETTINGS.md' \
      ':!README.md' >/dev/null 2>&1; then
    echo "❌ Secret-like content detected in tracked files."
    echo "   Pattern: $pattern"
    echo "   Remove or sanitize before merging."
    exit 1
  fi
done

exit 0
