# Ephemeral Databases for Agents (NXD) — Master Design

**Date:** 2026-05-21
**Status:** Draft
**Companion spec:** VXD's `2026-05-21-ephemeral-dbs-master-design.md` (kept in sync via port script)

## TL;DR

NXD gives every story a throwaway local Postgres database. The agent reads `DATABASE_URL` from `.nxd-db/connect.env` in its worktree, runs migrations and queries against it, and the database dies when the story finishes. Fully offline. Docker-backed.

This is the offline-first sibling of VXD's ephemeral-db feature. Where VXD chooses between Ghost (cloud) and Docker (local), **NXD only ships Docker** — staying true to NXD's offline-first principle.

## Why now

NXD already isolates agents at the file level via worktrees. It does not isolate them at the database level. Stories touching the same dev DB step on each other's writes, migration tests can't be re-run cleanly, and `DROP TABLE` is a footgun.

VXD's research into ghost.build crystallised the abstraction. NXD inherits the *interface* but implements only the offline backend.

## Goals

1. **Agent-first.** Auto-provision on story dispatch; no agent code needs to change.
2. **Human-visible.** `nxd db` CLI subtree, dashboard panel, metrics, audit trail.
3. **Offline-only.** Zero network calls. Docker daemon on localhost is the only dependency.
4. **Same interface as VXD.** Port script keeps `internal/devdb` in sync, minus the `ghost` subpackage.
5. **Cost-controlled.** Disk-usage tracking; auto-GC of orphans.
6. **Opt-in per project.** Default `provider: null`.

## Non-goals

- No Ghost provider. Ever. NXD stays offline-first.
- No cloud provider of any kind.
- No DB sharing between stories within a wave.
- No production database management.

## Architecture overview

```
                ┌──────────────────────────────────────────────┐
                │              Story lifecycle                  │
                │  STORY_ASSIGNED → STORY_DB_CREATED →          │
                │  STORY_STARTED → ... → STORY_COMPLETED →      │
                │  STORY_DB_DELETED                             │
                └──────────────────────────────────────────────┘
                                    │
                                    ▼
            ┌───────────────────────────────────────────────┐
            │       internal/devdb (mirrored from VXD)       │
            │  Provider interface: Create/Fork/Delete/...    │
            │  Lifecycle hooks, env injection contract       │
            └───────────────────────────────────────────────┘
                          │              │
                          ▼              ▼
              ┌──────────────┐ ┌──────────────┐
              │docker.Provider│ │null.Provider │
              │(only backend) │ │ no-op, tests │
              └──────────────┘ └──────────────┘
                          │
                          ▼
                  docker daemon (local)
                  pg_dump templates
                  CREATE DATABASE TEMPLATE
```

## What differs from VXD

| Aspect | VXD | NXD |
|--------|-----|-----|
| Providers shipped | ghost + docker + null | docker + null |
| MCP wrapper | Uses `ghost mcp` for Ghost; uses `vxd-db-mcp` for Docker | Only ships `nxd-db-mcp` (Docker-backed) |
| Preflight check | `CheckDevDBProviderReachable` (handles both) | Same code, gates `provider != docker` from being configured |
| CLI prefix | `vxd db ...` | `nxd db ...` |
| Naming | `vxd-<project>-<story-id>` | `nxd-<project>-<story-id>` |
| Config block | `devdb:` in vxd.yaml | `devdb:` in nxd.yaml |
| Config validation | Allows `provider: ghost\|docker\|null` | Allows `provider: docker\|null` only (rejects ghost with helpful error) |

Everything else matches.

## Sub-project decomposition (NXD)

NXD takes 5 PRs (no SP2):

| SP | Title | Status |
|----|-------|--------|
| 1 | devdb foundation (mirrored from VXD) | one-shot port |
| 3 | Docker provider (mirrored from VXD) | one-shot port |
| 4 | Per-story executor wiring (NXD-specific) | one-shot port + adjustments |
| 5 | QA migration gate (mirrored from VXD) | one-shot port |
| 6 | Human visibility (NXD-specific CLI + dashboard) | one-shot port + adjustments |

## Cross-cutting decisions

Same as VXD. Refer to VXD master spec sections:
- "Config shape" — same shape, `provider: docker|null` only
- "Event sourcing" — same three events with `nxd-` naming
- "Connection-string injection" — `.nxd-db/` instead of `.vxd-db/`
- "Failure modes and recovery" — same matrix; no Ghost-down case
- "Naming convention" — `nxd-` prefix
- "Secret handling" — Docker admin password file at `~/.nxd/devdb-admin.pw`
- "Cost model" — disk-usage tracking only, no compute-hours

## Agent-first vs human-visible

Same matrix as VXD with these substitutions:
- "Ghost MCP" entries removed
- All MCP install paths go through `nxd-db-mcp`
- `nxd db` CLI replaces `vxd db`

## Use-case matrix

Same as VXD's "Ghost / Docker offline" column. NXD is the Docker column only.

| Use case | Verdict |
|----------|---------|
| Per-story migration testing | ✅ |
| Schema-aware code generation | ✅ |
| Destructive SQL testing | ✅ |
| Multi-agent experimentation | ✅ |
| Long-running data pipelines | ⚠️ Phase-2 |
| Stateful integration tests | ⚠️ Mixed (cold-start overhead) |
| Pure-frontend stories | ❌ Skip |
| Production migrations | ❌ Don't |
| Offline development | ✅ |

## Risks (NXD-specific)

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Docker daemon unavailable on offline laptops | M | M | Preflight CRITICAL; clear "install docker" guidance |
| Disk fills from template + forks | M | M | GC routine; `nxd db status` shows disk pressure |
| Postgres image pull blocked offline | M | L | Document `docker pull postgres:16` as preflight prereq |
| Diverges from VXD over time | M | M | Port script in CI; wiring tests in both repos |
| Live Wave 3 tests need Docker on CI | M | L | Self-hosted runner or skip Wave 3 in CI; runs on dev laptops |

## Implementation order

1. **SP1 + SP3 (combined PR)** — port `internal/devdb` foundation + `docker` provider from VXD. No Ghost.
2. **SP4** — executor wiring + artifact protection + preflight checks.
3. **SP5** — QA migration gate (additive criteria).
4. **SP6** — `nxd db` CLI, dashboard panel, MCP wrapper, docs.
5. **Test waves** — Wave 1 in-PR, Wave 3 live (Docker laptop).

## Open questions

- Should NXD support a hypothetical "remote-docker" provider (ssh into a beefier machine)? Not now. Defer.
- pgvector / TimescaleDB image selection — document `nxd db template create --image timescale/...` as opt-in.

## References

- VXD master spec: `vortex-dispatch/docs/superpowers/specs/2026-05-21-ephemeral-dbs-master-design.md`
- ghost.build research: `vortex-dispatch/.firecrawl/ghost-build-*.md`
- VXD SP1 + SP3 specs (NXD inherits content): see VXD spec directory
- NXD offline-first principle: `CLAUDE.md`
