#!/usr/bin/env bash
set -euo pipefail

mode="ci"
tag="${GITHUB_REF_NAME:-}"
temp_config_created="false"

cleanup() {
  if [[ "$temp_config_created" == "true" ]]; then
    rm -f config.json
  fi
}
trap cleanup EXIT

if [[ "${1:-}" == "release" ]]; then
  mode="release"
  tag="${2:-$tag}"
fi

check_config_not_tracked() {
  if git ls-files --error-unmatch config.json >/dev/null 2>&1; then
    echo "❌ config.json must never be tracked by git."
    echo "   Run: git rm --cached config.json"
    exit 1
  fi
}

check_gofmt() {
  local fmt_files
  fmt_files=$(gofmt -l .)
  if [[ -n "$fmt_files" ]]; then
    echo "❌ Files not formatted with gofmt:"
    echo "$fmt_files"
    exit 1
  fi
}

check_tag_semver() {
  if [[ -z "$tag" ]]; then
    echo "❌ Release mode requires a tag (example: v1.2.3)."
    exit 1
  fi

  if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z-]+)*$ ]]; then
    echo "❌ Invalid release tag format: $tag"
    echo "   Expected semantic tag like v1.2.3"
    exit 1
  fi
}

check_changelog_entry() {
  if [[ ! -f CHANGELOG.md ]]; then
    echo "❌ CHANGELOG.md is missing."
    exit 1
  fi

  if ! grep -Eq "^##[[:space:]]+${tag//./\.}([[:space:]]|$|-)" CHANGELOG.md; then
    echo "❌ CHANGELOG.md missing section for ${tag}."
    echo "   Add a header like: ## ${tag} - YYYY-MM-DD"
    exit 1
  fi
}

ensure_test_config() {
  if [[ -f config.json ]]; then
    return
  fi

  if [[ -f config.example.json ]]; then
    cp config.example.json config.json
    temp_config_created="true"
    echo "ci_guard: created temporary config.json from config.example.json"
    return
  fi

  echo "❌ Neither config.json nor config.example.json is available."
  exit 1
}

chmod +x scripts/secret_scan.sh
scripts/secret_scan.sh --repo
check_config_not_tracked
ensure_test_config
check_gofmt
go vet ./...
go test -race -count=1 ./...
go build ./...

if [[ "$mode" == "release" ]]; then
  check_tag_semver
  check_changelog_entry
fi

echo "ci_guard: ${mode} checks passed"
