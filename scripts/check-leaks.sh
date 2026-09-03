#!/usr/bin/env bash
# scripts/check-leaks.sh — fail if private terms slip into the public repo.
#
# The forbidden terms are deliberately NOT stored in this file: a public
# script that lists the client names, employer domain and home path it is
# meant to keep out of the repo leaks them itself. Terms are read from:
#
#   1. scripts/.leak-terms      (git-ignored; one `<regex>|<description>` per
#                                line, `#` comments and blank lines ignored)
#   2. $LEAK_TERMS              (same format, newline-separated — CI passes
#                                the LEAK_TERMS repository secret this way)
#
# Both sources are merged when present. When neither exists the check is
# SKIPPED with a visible warning rather than silently passing, so a fork
# without the private list still gets a green build but the maintainers'
# CI (which has the secret) enforces the gate. Matching terms are never
# echoed back — only the description and the offending file:line.
#
# Run locally with `make leak-check`. See CONTRIBUTING.md.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TERMS_FILE="${LEAK_TERMS_FILE:-$ROOT/scripts/.leak-terms}"

# Files that are allowed to match (this script itself + curated allowlist).
ALLOWFILES=(
  'scripts/check-leaks.sh'
  'scripts/.leak-terms'
  '.git/'
  'docs/history/'  # historical archived requirements
)

FORBIDDEN=()
load_terms() {
  local line
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%%$'\r'}"
    [[ -z "${line// /}" || "$line" == \#* ]] && continue
    [[ "$line" == *"|"* ]] || { echo "::warning::leak-check: ignoring term without '|description'"; continue; }
    FORBIDDEN+=("$line")
  done
}

if [[ -f "$TERMS_FILE" ]]; then
  load_terms < "$TERMS_FILE"
fi
if [[ -n "${LEAK_TERMS:-}" ]]; then
  load_terms <<< "$LEAK_TERMS"
fi

if [[ ${#FORBIDDEN[@]} -eq 0 ]]; then
  echo "::warning::leak-check: no terms configured (scripts/.leak-terms missing and LEAK_TERMS unset) — check skipped"
  exit 0
fi

fail=0
for entry in "${FORBIDDEN[@]}"; do
  pattern="${entry%%|*}"
  desc="${entry#*|}"
  # Search tracked files only; skip the allowlist. The pattern itself is
  # never printed.
  matches=$(cd "$ROOT" && git ls-files | grep -vE "$(IFS=\|; echo "${ALLOWFILES[*]}")" \
            | xargs grep -IlE -- "$pattern" 2>/dev/null || true)
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

echo "leak-check: OK (${#FORBIDDEN[@]} terms checked)"
