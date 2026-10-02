#!/usr/bin/env bash
set -euo pipefail

mode="staged"

usage() {
  cat <<'EOF'
Usage:
  scripts/secret_scan.sh [--staged|--repo]

Modes:
  --staged   Scan the git index (default): what is about to be committed.
  --repo     Scan every tracked file in the working tree.

Rules:
  * Tier 1 (high-confidence secret shapes) is scanned with no exclusions at
    all: tests, README, docs and examples included. A real token of one of
    these shapes is never legitimate in a tracked file.
  * Tier 2 (credential-shaped config keys) skips *_test.go files, where a
    literal value is the whole point of the test, and ignores documented
    placeholder values everywhere.
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

if ! command -v git >/dev/null 2>&1; then
  echo "❌ git is required by secret_scan.sh"
  exit 1
fi

if ! command -v grep >/dev/null 2>&1; then
  echo "❌ grep is required by secret_scan.sh"
  exit 1
fi

# Tier 1: high-confidence credential shapes. Never excluded, never filtered.
secret_patterns=(
  '[0-9]{8,}:[A-Za-z0-9_-]{25,}'                   # Telegram bot token
  'AIza[0-9A-Za-z_-]{20,}'                         # Google API key
  'gh[pousr]_[A-Za-z0-9]{36,}'                     # GitHub classic PAT
  'github_pat_[A-Za-z0-9_]{40,}'                   # GitHub fine-grained PAT
  'AKIA[0-9A-Z]{16}'                               # AWS access key id
  'xox[baprs]-[A-Za-z0-9-]{10,}'                   # Slack token
  '-----BEGIN (RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----'
)

# Tier 2: config keys whose value is a credential. Skipped in *_test.go only.
config_key_patterns=(
  '"bot_token"\s*:\s*"[^"]{10,}"'
  '"gemini_api_key"\s*:\s*"[^"]{10,}"'
)

test_pathspecs=(':!*_test.go')

# Values that are documentation, not credentials.
placeholder_pattern='YOUR_[A-Za-z0-9_]*|your_[a-z0-9_]*|CHANGEME|CHANGE_?ME|REPLACE[_-]?ME|PLACEHOLDER|EXAMPLE|SAMPLE|DUMMY|FAKE|FILL[_-]?ME|INSERT[_-]|<[A-Za-z_]+>|\.\.\.'

GIT_GREP_HITS=""

# git_grep_files <rev_flag> <pattern> [pathspecs...]
# Sets GIT_GREP_HITS to the matching paths.
# Returns 0 on match, 1 on "no match", 2 on a git usage/internal error.
# NOTE: -e is mandatory, several patterns start with '-' and git grep would
# otherwise parse them as options.
git_grep_files() {
  local rev_flag="$1" pattern="$2"
  shift 2
  local out="" status=0

  # rev_flag is deliberately unquoted: it is either "--cached" or "".
  # shellcheck disable=SC2086
  out=$(git grep $rev_flag -l -E -e "$pattern" -- "$@" 2>&1) || status=$?

  if [[ $status -eq 0 ]]; then
    GIT_GREP_HITS="$out"
    return 0
  fi

  GIT_GREP_HITS=""
  if [[ $status -eq 1 ]]; then
    return 1
  fi

  echo "❌ secret_scan: git grep failed (exit $status) on pattern: $pattern"
  echo "$out"
  return 2
}

# git_show_blob <rev_flag> <path> -> blob content.
# rev_flag "--cached" reads the index, empty string reads the working tree.
git_show_blob() {
  local rev_flag="$1" file="$2"
  if [[ -n "$rev_flag" ]]; then
    git cat-file blob ":$file"
  else
    cat -- "$file"
  fi
}

report_hits() {
  local context="$1" pattern="$2"
  shift 2
  local -a files=("$@")
  echo "❌ Secret-like content detected ($context)."
  echo "   Pattern: $pattern"
  if [[ ${#files[@]} -gt 0 ]]; then
    echo "   Files:"
    printf '     %s\n' "${files[@]}"
  fi
  echo "   Remove the secret, or replace it with a documented placeholder."
}

scan_secret_patterns() {
  local rev_flag="$1" context="$2" pattern status
  local -a hits=()

  for pattern in "${secret_patterns[@]}"; do
    hits=()
    status=0
    git_grep_files "$rev_flag" "$pattern" || status=$?
    case "$status" in
    0)
      mapfile -t hits <<<"$GIT_GREP_HITS"
      report_hits "$context" "$pattern" "${hits[@]}"
      exit 1
      ;;
    1) ;;
    *)
      exit 1
      ;;
    esac
  done
}

scan_config_key_patterns() {
  local rev_flag="$1" context="$2"
  shift 2
  local -a pathspecs=("$@")
  local pattern file status
  local -a candidates=() hits=() matched_lines=() non_placeholder=()

  for pattern in "${config_key_patterns[@]}"; do
    hits=()
    status=0
    git_grep_files "$rev_flag" "$pattern" "${pathspecs[@]}" || status=$?
    case "$status" in
    0) mapfile -t candidates <<<"$GIT_GREP_HITS" ;;
    1) candidates=() ;;
    *)
      exit 1
      ;;
    esac

    for file in "${candidates[@]}"; do
      mapfile -t matched_lines < <(git_show_blob "$rev_flag" "$file" | grep -E -e "$pattern" || true)
      if [[ ${#matched_lines[@]} -eq 0 ]]; then
        continue
      fi
      # Flag only if at least one matching value is NOT a documented placeholder.
      mapfile -t non_placeholder < <(printf '%s\n' "${matched_lines[@]}" | grep -Ev -e "$placeholder_pattern" || true)
      if [[ ${#non_placeholder[@]} -gt 0 ]]; then
        hits+=("$file")
      fi
    done

    if [[ ${#hits[@]} -gt 0 ]]; then
      report_hits "$context" "$pattern" "${hits[@]}"
      exit 1
    fi
  done
}

run_scan() {
  local rev_flag="" context="tracked files"

  if [[ "$mode" == "staged" ]]; then
    rev_flag="--cached"
    context="staged content"
  fi

  scan_secret_patterns "$rev_flag" "$context"
  scan_config_key_patterns "$rev_flag" "$context" "${test_pathspecs[@]}"
}

if [[ -z "$(git ls-files 2>/dev/null || true)" ]]; then
  echo "secret_scan: nothing tracked, nothing to scan"
  exit 0
fi

run_scan

echo "secret_scan: ${mode} scan passed"
exit 0