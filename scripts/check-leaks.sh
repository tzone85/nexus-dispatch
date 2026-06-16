#!/usr/bin/env bash
# scripts/check-leaks.sh — fail if private terms slip into the public repo.
#
# Run locally with `make leak-check` or via the `leak-check` CI job. The
# allowlist is conservative — add new terms here as you discover them, do
# NOT relax them.
set -euo pipefail

# Each entry is `<regex>|<description>`. Regex uses extended grep syntax.
FORBIDDEN=(
  'mukuru|client name (Mukuru) — redact to a generic placeholder'
  'cashtask|client name (CashTask) — redact to a generic placeholder'
  'sanlam\.co\.za|corporate employer email domain'
  '/Users/mncedimini|hardcoded personal $HOME path — use runtime detection'
  'tzone85/project-x|private personal repo reference'
)

# Files that are allowed to match (this script itself + curated allowlist).
ALLOWFILES=(
  'scripts/check-leaks.sh'
  '.git/'
  'docs/history/'  # historical archived requirements
)

fail=0
for entry in "${FORBIDDEN[@]}"; do
  pattern="${entry%%|*}"
  desc="${entry#*|}"
  # Search tracked files only; skip the allowlist.
  matches=$(git ls-files | grep -vE "$(IFS=\|; echo "${ALLOWFILES[*]}")" \
            | xargs grep -InE "$pattern" 2>/dev/null || true)
  if [[ -n "$matches" ]]; then
    echo "::error::leak-check found forbidden term — $desc"
    echo "$matches"
    fail=1
  fi
done

if [[ "$fail" -ne 0 ]]; then
  echo
  echo "Fix the matches above, or update scripts/check-leaks.sh ALLOWFILES if"
  echo "the match is genuinely safe to ship publicly."
  exit 1
fi

echo "leak-check: OK"
