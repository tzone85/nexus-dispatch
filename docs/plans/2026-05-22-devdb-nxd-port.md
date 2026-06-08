# NXD DevDB Port Plan

> **Status:** queued. Mirror VXD's SP1+SP3+SP4+SP5+SP6 work (minus SP2 Ghost — NXD stays offline). Each task here is a 1:1 port of a VXD task with the changes noted below.

## Source

VXD on main as of 2026-05-22, commits a8cbef1..9a46d47. Master specs lived in each repo's docs/superpowers/specs/2026-05-21-ephemeral-dbs-master-design.md (since removed; see CHANGELOG for the shipped outcome).

## Differences from VXD

- Package prefix: `nxd-` instead of `vxd-` (use `devdb.PrefixNXD = "nxd"` — add this constant during port).
- No Ghost provider — `internal/devdb/ghost/` directory not created.
- CLI prefix: `nxd db ...` instead of `vxd db ...`.
- Worktree directory: `.nxd-db/` instead of `.vxd-db/`.
- Project name discovery: NXD project name from `cfg.Workspace.StateDir` basename.
- Config block name: `devdb:` (same key).

## Task list (mirror)

1. **NXD-SP1+SP3-foundation+docker** — port `internal/devdb/`, `internal/devdb/null/`, `internal/devdb/docker/`, the SQL projection table, events, config validation. Single PR.
2. **NXD-SP4-A: cleanup followups** — apply the same prefix-constant + `emitFailed` db_id fix.
3. **NXD-SP4-B: artifact strip** — add `.nxd-db/` to NXD's stripVXDArtifactsFromBranch equivalent.
4. **NXD-SP4-1..6: executor wiring** — mirror VXD SP4-1 through SP4-6 (Lifecycle injection, post-merge Release, resume orphan recovery, SLA-paths Release, preflight checks, CLI bootstrap).
5. **NXD-SP5: QA gate criteria** — port migration_succeeds / schema_changed / sql_query_returns.
6. **NXD-SP6: visibility** — `nxd db` CLI subtree + template subgroup + configurable docker host.

## Open questions for the port session

- Is NXD's `monitor.go` shape identical to VXD's? If diverged, ports may need adjustments.
- NXD's existing `_test.go` files use what package convention?

## Estimated effort

~12-18 atomic commits. Same TDD discipline as VXD: each Provider/Lifecycle change gets a wiring test before code lands.

## Execute when

User signals NXD readiness. Typically: after VXD's SP2 lands (so the full surface is settled), or when NXD needs a real DB-touching client project (mukuru-api-equivalent).
