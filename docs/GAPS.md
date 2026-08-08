# nexus-dispatch brownfield gauntlet — Crucible gap register

Legitimacy run of the brownfield gauntlet (`crucible/docs/testing-legitimacy.md`), under the fable-5-playbook method (fresh-context verification, evidence-grounded claims, no sanding down). Grades every Crucible promise KEPT / HEROICS / BROKEN. NXD defect fixes and new work live on local branches `crux/fix-recent-events-and-projection-rebuild` and `crux/brownfield-perf-feature-refactor`; nothing pushed.

## Factory promises, graded

| Promise | Verdict | Evidence |
|---|---|---|
| `crux req` auto-syncs target repos before planning (real SHAs) | KEPT | 2 repos synced, REPO_SYNCED with before/after SHAs |
| Sync survives a dirty edit (autostash) | KEPT | dirty README marker survived a re-sync |
| Sync conflict → skipped-dirty-conflict, repo restored | KEPT | live drill on a throwaway bare-origin fixture: skipped-dirty-conflict, HEAD unchanged, edit restored, zero markers |
| `crux doctor` / `config validate` / `sandbox probe` | KEPT | doctor 9 checks, config valid |
| Role routing (investigator×2, debugger, security, performance, builder, refactorer, scribe) | KEPT (after G4) | item-7 mis-routed to builder → fixed → re-dispatched to refactorer, verified live |
| Story fail → draft → re-dispatch | KEPT | proved on item-7; escalation ladder never climbs (BROKEN, architectural) |
| Investigator brief helps a real investigation | KEPT | mapped a 485-file module, found the highest-coupling area (engine.Monitor) and 2 substantiated defects |
| Debugger brief drives repro-first fixes | KEPT | both NXD defects fixed failing-test-first; independently re-verified (`internal/state -race` green, both repro tests pass) |
| `crux security scan --gate` | KEPT (with a scale caveat) | 71 findings; the critical/high ones are NXD's own sanitize/injection-DEFENSE code + tests + AGENTS.md — false positives by construction. G5's per-line `crux-allow` is the fix; a path-allowlist for security-test files is the scale follow-up (backlog). |
| `crux state rebuild` lossless replay | KEPT | status byte-identical before/after on the nexus workspace |
| `crux dashboard export` | KEPT | nexus-dash.html written |
| REQ_COMPLETED on all-stories-done | KEPT | fired after item-8 |
| Budget spend tracking | BROKEN | `$0.00 SPENT` — operator mode executes nothing to meter (architectural, shared with greenfield) |

## Real NXD bugs the factory found and fixed (brownfield value, separate from Crucible's own gaps)

- **DEFECT 1** — `state.FileStore` `Limit` returned the OLDEST N, not newest → every live-tail view (web snapshot, dashboard feed, Hub delta push) frozen on the first events. Fixed: tail semantics. Test `TestFileStore_Limit_ReturnsMostRecent`. Perf item quantified the deeper cost: `List(Limit=50)` still pays the full-log scan (78ms @50k) — the limit bounds the result, not the work.
- **DEFECT 2** — projection desync unrecoverable; `emitEventOrLog` documented a "replay recovers on startup" contract that did not exist. Fixed: `SQLiteStore.RebuildFrom` + a watermark + `loadStores` rebuild-if-behind. Test `TestRebuildFrom_RecoversDesyncedProjection`. New feature (item 6): `nxd doctor` projection-drift check surfaces the desync before it bites.

Sibling note: DEFECT 2 is exactly the contract **Crucible itself has** and which this gauntlet re-verified lossless via `crux state rebuild` — the factory caught in its cousin a gap it had already closed in itself.

## Verdict

Crucible ran a real brownfield adoption end to end: sync → investigate → debug (2 real bugs, repro-first) → security → perf → feature → refactor → writeup → REQ_COMPLETED, every gate green. HEROICS: the worker agents executed the briefs; Crucible planned, routed, gated, and audited but did not spawn them. BROKEN (one root cause, shared with greenfield): no embedded executor, so budget meters nothing and escalation never climbs. Crucible fixes shipped this gauntlet: G4 refactorer routing, G5 scanner suppression (G1–G3 landed in the greenfield run).
