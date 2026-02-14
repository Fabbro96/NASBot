#!/usr/bin/env bash
set -euo pipefail

mode="staged"

usage() {
  cat <<'EOF'
Usage:
  scripts/secret_scan.sh [--staged|--repo]

Modes:
  --staged   Scan staged diff only (default)
  --repo     Scan tracked repository files
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
  --staged)
    mode="staged"
    shift
    ;;
  --repo)
    mode="repo"
    shift
    ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    echo "Unknown argument: $1"
    usage
    exit 1
    ;;
  esac
done

patterns=(
  '"bot_token"\s*:\s*"[^"]{10,}"'
  '"gemini_api_key"\s*:\s*"[^"]{10,}"'
  '[0-9]{8,}:[A-Za-z0-9_-]{25,}'
  'AIza[0-9A-Za-z\-_]{20,}'
)

repo_exclusions=(
  ':!*_test.go'
  ':!config.example.json'
  ':!CHANGELOG_SETTINGS.md'
  ':!README.md'
)

scan_text_for_patterns() {
  local text="$1"
  for pattern in "${patterns[@]}"; do
    if printf "%s" "$text" | grep -E -q "$pattern"; then
      echo "❌ Secret-like content detected in staged changes."
      echo "   Pattern: $pattern"
      echo "   Remove secrets before committing."
      exit 1
    fi
  done
}

scan_repo_for_patterns() {
  for pattern in "${patterns[@]}"; do
    if git grep -nE "$pattern" -- "${repo_exclusions[@]}" >/dev/null 2>&1; then
      echo "❌ Secret-like content detected in tracked files."
      echo "   Pattern: $pattern"
      echo "   Remove or sanitize before merging."
      exit 1
    fi
  done
}

if [[ "$mode" == "staged" ]]; then
  staged_files=$(git diff --cached --name-only --diff-filter=ACM)
  if [[ -z "${staged_files}" ]]; then
    exit 0
  fi

  staged_content=$(git diff --cached | cat)
  scan_text_for_patterns "$staged_content"
  exit 0
fi

if [[ -z "$(git ls-files)" ]]; then
  exit 0
fi

scan_repo_for_patterns

exit 0
