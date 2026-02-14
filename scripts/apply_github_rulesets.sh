#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd)"
cd "$REPO_ROOT"

repo=""
dry_run="false"

usage() {
  cat <<'EOF'
Usage:
  scripts/apply_github_rulesets.sh [--repo owner/name] [--dry-run]

Examples:
  scripts/apply_github_rulesets.sh --dry-run
  scripts/apply_github_rulesets.sh --repo Fabbro96/NASBot
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      repo="${2:-}"
      shift 2
      ;;
    --dry-run)
      dry_run="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown arg: $1"
      usage
      exit 1
      ;;
  esac
done

if ! command -v python >/dev/null 2>&1; then
  echo "❌ python is required to parse ruleset templates"
  exit 1
fi

if [[ -z "$repo" ]]; then
  if ! git remote get-url origin >/dev/null 2>&1; then
    echo "❌ Cannot detect repository. Use --repo owner/name"
    exit 1
  fi
  repo=$(git remote get-url origin | sed -E 's#(git@github.com:|https://github.com/)##; s#\.git$##')
fi

has_gh="true"
if ! command -v gh >/dev/null 2>&1; then
  has_gh="false"
fi

if [[ "$dry_run" != "true" && "$has_gh" != "true" ]]; then
  echo "❌ gh CLI is required to apply rulesets: https://cli.github.com/"
  exit 1
fi

if [[ "$dry_run" != "true" ]]; then
  gh auth status >/dev/null
fi

template_exists() {
  local path="$1"
  if [[ ! -f "$path" ]]; then
    echo "❌ Missing template: $path"
    exit 1
  fi
}

apply_ruleset() {
  local file="$1"

  template_exists "$file"

  local name target
  name=$(python - <<PY
import json
with open("$file", "r", encoding="utf-8") as f:
    print(json.load(f)["name"])
PY
)
  target=$(python - <<PY
import json
with open("$file", "r", encoding="utf-8") as f:
    print(json.load(f)["target"])
PY
)

  local existing_id
  existing_id=""
  if [[ "$has_gh" == "true" ]]; then
    existing_id=$(gh api "repos/${repo}/rulesets" --paginate --jq ".[] | select(.name==\"${name}\" and .target==\"${target}\") | .id" || true)
    existing_id=$(echo "$existing_id" | head -n1 | tr -d '\r')
  fi

  if [[ "$dry_run" == "true" ]]; then
    if [[ "$has_gh" != "true" ]]; then
      echo "[dry-run] validated template '${name}' (${target}) from ${file}"
      echo "[dry-run] gh CLI not found; cannot query existing remote rulesets"
    elif [[ -n "$existing_id" ]]; then
      echo "[dry-run] would update ruleset '${name}' (id=${existing_id}) from ${file}"
    else
      echo "[dry-run] would create ruleset '${name}' from ${file}"
    fi
    return
  fi

  if [[ -n "$existing_id" ]]; then
    gh api --method PUT "repos/${repo}/rulesets/${existing_id}" --input "$file" >/dev/null
    echo "✅ Updated ruleset '${name}' (id=${existing_id})"
  else
    gh api --method POST "repos/${repo}/rulesets" --input "$file" >/dev/null
    echo "✅ Created ruleset '${name}'"
  fi
}

apply_ruleset ".github/rulesets/main-protection.json"
apply_ruleset ".github/rulesets/release-tags-protection.json"

echo "Done. Rulesets are aligned for ${repo}."
