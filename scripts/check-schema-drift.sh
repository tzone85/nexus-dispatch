#!/usr/bin/env bash
# scripts/check-schema-drift.sh — keep migrations/001_init.sql in lockstep
# with the schema NXD actually applies at runtime.
#
# internal/state/sqlite.go owns the schema: the `initSQL` constant (CREATE
# TABLE / CREATE INDEX / INSERT bootstrap) plus a series of idempotent
# `ALTER TABLE … ADD COLUMN …` statements that upgrade databases created by
# older builds. migrations/001_init.sql exists for external tooling and has
# drifted twice (missing stories.acceptance_criteria, story_databases,
# projection_meta). This script extracts every statement from sqlite.go,
# renders the expected migration file, and diffs it against the committed one.
#
#   bash scripts/check-schema-drift.sh          # verify (CI + `make check`)
#   bash scripts/check-schema-drift.sh --write  # regenerate migrations/001_init.sql
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/internal/state/sqlite.go"
DST="$ROOT/migrations/001_init.sql"

[[ -f "$SRC" ]] || { echo "schema-drift: $SRC not found" >&2; exit 2; }

render() {
  echo "-- GENERATED from internal/state/sqlite.go by scripts/check-schema-drift.sh."
  echo "-- Do not edit by hand: run \`bash scripts/check-schema-drift.sh --write\`."
  echo "-- CI fails when this file and the Go schema disagree."
  echo
  # 1. The initSQL constant: everything between the opening and closing
  #    backtick of `const initSQL = \`…\``.
  awk '
    /^const initSQL = `/ { inblock = 1; next }
    inblock && /^`/      { inblock = 0; exit }
    inblock              { print }
  ' "$SRC" | sed -e 's/[[:space:]]*$//' | awk 'NF || prev { print } { prev = NF }'
  echo
  echo "-- Column upgrades NewSQLiteStore applies on every open, in source order."
  echo "-- SQLite has no ADD COLUMN IF NOT EXISTS: the Go code ignores the"
  echo "-- 'duplicate column name' error each statement raises on an up-to-date"
  echo "-- database, so external tooling must run these with errors ignored too."
  # 2. Every ALTER TABLE statement, in source order, whether it appears in a
  #    db.Exec(`…`) call or as a quoted string in a slice.
  grep -oE 'ALTER TABLE [^`"]*' "$SRC" | sed -e 's/[[:space:]]*$//' -e 's/$/;/'
}

expected="$(render)"

if [[ "${1:-}" == "--write" ]]; then
  printf '%s\n' "$expected" > "$DST"
  echo "schema-drift: wrote $DST"
  exit 0
fi

if [[ ! -f "$DST" ]]; then
  echo "::error::schema-drift: $DST is missing — run: bash scripts/check-schema-drift.sh --write"
  exit 1
fi

if ! diff -u "$DST" <(printf '%s\n' "$expected"); then
  echo
  echo "::error::schema-drift: migrations/001_init.sql does not match internal/state/sqlite.go"
  echo "Regenerate with: bash scripts/check-schema-drift.sh --write"
  exit 1
fi

echo "schema-drift: OK (migrations/001_init.sql matches internal/state/sqlite.go)"
