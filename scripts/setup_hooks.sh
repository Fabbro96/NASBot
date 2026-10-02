#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd)"
HOOKS_DIR="$REPO_ROOT/.githooks"

echo "Configuring local git hooks..."

# Pre-commit: fast checks (gofmt, quality_check)
cat << 'EOF' > "$HOOKS_DIR/pre-commit"
#!/usr/bin/env bash
set -euo pipefail

echo "Running pre-commit checks..."

# Check if gofmt needs to be run
fmt_files=$(gofmt -l .)
if [[ -n "$fmt_files" ]]; then
  echo "❌ Files not formatted with gofmt. Run 'gofmt -w .' or format your code."
  echo "$fmt_files"
  exit 1
fi

# Run fast quality checks and secret scan
scripts/secret_scan.sh --repo
scripts/quality_check.sh

echo "✅ pre-commit checks passed"
EOF
chmod +x "$HOOKS_DIR/pre-commit"

# Pre-push: full CI suite (tests, build, vet)
cat << 'EOF' > "$HOOKS_DIR/pre-push"
#!/usr/bin/env bash
set -euo pipefail

echo "Running pre-push checks (CI Guard)..."

# Run the full CI guard script
scripts/ci_guard.sh

echo "✅ pre-push checks passed"
EOF
chmod +x "$HOOKS_DIR/pre-push"

# Commit-msg: enforce conventional commits
cat << 'EOF' > "$HOOKS_DIR/commit-msg"
#!/usr/bin/env bash
set -euo pipefail

commit_msg_file=$1
commit_msg=$(cat "$commit_msg_file")

# Skip for merge commits
if [[ "$commit_msg" =~ ^Merge.* ]]; then
  exit 0
fi

# Regex for Conventional Commits
pattern="^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)(\([a-z0-9_-]+\))?: .+"

if ! [[ "$commit_msg" =~ $pattern ]]; then
  echo "❌ Error: Commit message does not follow Conventional Commits format."
  echo "Expected format: <type>(<scope>): <subject>"
  echo "Allowed types: feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert"
  echo "Example: feat(api): add new endpoint"
  echo ""
  echo "Your commit message:"
  echo "$commit_msg"
  exit 1
fi
EOF
chmod +x "$HOOKS_DIR/commit-msg"

echo "  - commit-msg: enforces Conventional Commits standard"

# Activate the hooks. Without this the files above are inert: git never runs
# them, so no Conventional Commits check, no secret scan and no quality gate
# happen locally.
if ! command -v git >/dev/null 2>&1; then
  echo "❌ git is required to configure core.hooksPath"
  echo "   Hooks were written to $HOOKS_DIR but are NOT active."
  exit 1
fi

if ! git -C "$REPO_ROOT" rev-parse --git-dir >/dev/null 2>&1; then
  echo "❌ $REPO_ROOT is not inside a git repository."
  echo "   Hooks were written to $HOOKS_DIR but are NOT active."
  exit 1
fi

if git -C "$REPO_ROOT" config core.hooksPath .githooks; then
  echo "✅ core.hooksPath set to .githooks (local config, not committed)"
  echo "   Hooks now run on commit and push."
else
  echo "❌ Failed to set core.hooksPath. Hooks are NOT active."
  echo "   Run manually: git -C $REPO_ROOT config core.hooksPath .githooks"
  exit 1
fi

echo ""
echo "✅ Git hooks configured and active:"
echo "  - pre-commit: runs gofmt, quality check and secret scan"
echo "  - pre-push: runs the full test suite (ci_guard.sh)"
echo "  - commit-msg: enforces Conventional Commits standard"
echo ""
echo "To bypass temporarily: git commit --no-verify"
echo "To disable:         git config --unset core.hooksPath"
