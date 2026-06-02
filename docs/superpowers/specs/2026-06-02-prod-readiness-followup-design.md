# Production-readiness follow-up — Design

**Date:** 2026-06-02
**Status:** Implemented
**Companion specs:** [[2026-05-21-ephemeral-dbs-master-design]], [[ARCHITECTURE]]

## TL;DR

Closes the four remaining items called out in `CLAUDE.md` "Current State" as production-readiness blockers after the devdb master design landed:

1. **`req-logs` + `req --background`** — daemonisation surface so requirement runs survive parent-shell teardown / macOS app-nap (ported from VXD).
2. **Tech-Lead conflict resolver + post-merge integration build + binary strip** — three-way merge automation gated by `git numstat` binary-conflict detection (ported from VXD).
3. **Per-story devdb dashboard column + Databases panel** — `state.ListStoryDatabases` + `StoryDB` snapshot field + `DB` column in stories table + aggregate `db_summary` panel.
4. **Coverage backfill** — restore CLI / criteria / devdb-docker per-package coverage after the recent feature batches.

This spec is the audit trail. Nothing here is new behaviour — it's the production-readiness commitment to keep the test surface, docs, and dashboard aligned with what merged into `main`.

## Why now

The user goal `/goal "Please get this to be production ready..."` exposed three real gaps:

- The `STORY_DB_CREATED/FAILED/DELETED` projection had no read API and no UI surface, so operators couldn't see lifecycle state without `sqlite3 ~/.nxd/nxd.db`.
- Recent ports (`db.go`, `req-logs.go`, `req.go --background`, `resume.go` devdb wiring, `criteria/db.go`) landed without matching unit tests, dropping per-package coverage below the CLAUDE.md baseline.
- The CLI reference and changelog didn't mention any of the merged commands.

## Goals

1. Add a projection read API for `story_databases` and surface it on the web dashboard.
2. Backfill unit tests for the new pure-Go surface (helpers, guards, validation paths) without standing up live infra.
3. Sync `CHANGELOG.md`, `docs/reference/cli-reference.md`, and `docs/reference/event-reference.md` with what merged.
4. Document the architectural ceilings that prevent some packages from reaching 95% in the default lane (live PG / live Docker daemon required).

## Non-goals

- New devdb providers. Docker stays the only backend (see [[2026-05-21-ephemeral-dbs-master-design]] §"Why now").
- Live-PG / live-Docker integration in the default lane. Both follow the `live_tmux`-style precedent — a build-tagged CI lane (`integration` for docker, `live_pg` future) covers the network-bound branches.
- Refactoring `internal/cli/resume.go` runResume happy path for testability. The ROI was lower than docs + dashboard wiring; revisit if it regresses again.

## What landed

### Projection read API

`state.SQLiteStore.ListStoryDatabases(StoryDBFilter)` — accepts `StoryID` and `Status` filters, returns `[]StoryDatabase` ordered by `created_at DESC` so dashboards surface the freshest row per story first.

Backing tests in `internal/state/story_databases_test.go` cover: empty store, single `STORY_DB_CREATED` projection, filter-by-story, filter-by-status, and the create → delete sequence (duration / bytes propagation).

### Dashboard wiring

`web.StateSnapshot` grows two optional fields:

- `StoryDBs map[string]StoryDB` — keyed by story ID, freshest row per story.
- `DBSummary *DBSummary` — aggregate `{created, failed, deleted}` counts.

Both are `omitempty`-marshaled so projects with `devdb.provider: null` (the default) emit the same JSON shape they always did. The static front-end renders the `DB` column inline with the stories table and the `Databases` panel above the activity log; both hide themselves when the snapshot fields are absent.

### Coverage targets

| Package           | Before | After  | Gap reason                                      |
|-------------------|--------|--------|-------------------------------------------------|
| `internal/cli`    | 68.0%  | ~71%   | runResume/runDashboard happy paths need full pipeline fixtures (worktree + LLM client chain + tmux). |
| `internal/criteria` | 67.2% | 71.6%  | `evaluateSQLQueryReturns`, `evaluateSchemaChanged`, `dumpSchemaText` need a live Postgres. Same precedent as `live_tmux`. |
| `internal/devdb/docker` | 42.0% | 45.1%  | Provider methods need a live Docker daemon + Postgres protocol; covered by the `integration` build-tagged lane. |

The default CI lane stays at the 50% floor with per-package ratcheting. A `live_pg` build tag mirroring `live_tmux` is the obvious next step if criteria coverage becomes blocking.

## How to apply

- When a new event type lands in `state/events.go` with a `projectXxx` SQLite handler, add a matching `ListXxx(Filter)` read API in the same commit and an opt-in snapshot field in `web/data.go`.
- New CLI subcommands must land with a section in `docs/reference/cli-reference.md` and an entry in `CHANGELOG.md` "Unreleased" — the post-merge integration build will fail on docs drift once the doc-lint job is wired (next milestone).
- Architectural ceilings on coverage must be recorded in `CLAUDE.md` "Per-Package Coverage" with the exact reason (build tag / live infra / cobra interactive path).

## Risks / open questions

- The web dashboard's `DB` column adds one more cell per story row. Heavy-load runs with 100+ stories may want column virtualisation; current pageful is ~50 rows so not urgent.
- `STORY_DB_DELETED` with `status: "kept"` (devdb keep-on-failure) is handled by the projection but not currently surfaced as a distinct dashboard state — it renders as `deleted`. Open issue: split into a `kept` chip if operators ask.
- `DBSummary` counts orphan-recovered rows as `deleted`. Could be split if orphan recovery becomes a distinct ops concern.
