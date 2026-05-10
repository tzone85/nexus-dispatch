# Coverage Roadmap to 95% Total

> Multi-PR plan for raising NXD's test coverage from 77.8% (May 2026) to ≥95% total. Each PR is independently mergeable, has a measured before/after, and ships only **meaningful** tests — boundary, error paths, regressions — not happy-path padding.

## Current state (post round 2 — `556e855`)

```
Total: 77.8% across 27 packages.
```

| Tier | Packages |
|---|---|
| ≥95% | sanitize (100), memory (99), graph (96), nlog (96) |
| 90–95% | improver (94), llm (91), config (90), metrics (90), plugin (90), update (91), artifact (90) |
| 80–90% | routing (89), agent (86), codegraph (86), dashboard (84), scratchboard (84), criteria (84), git (83), runtime (83), state (82), repolearn (81) |
| <80% | web (78), engine (74), tmux (73), cli (66) |

**The math.** Codebase is ~30k Go statements. Total = sum(covered) / sum(total). To reach 95% from 77.8% we need ~5,100 more covered statements. That's the size of the work below.

## Why each blocked package is blocked

| Package | Block | Path to fix |
|---|---|---|
| `cli` (66%) | Cobra/Bubbletea coupling — interactive TUI hard to drive in tests | Extract `runFoo` business logic from Cobra glue; inject store/LLM via interfaces (PR 1, PR 3) |
| `engine` (74%) | `spawn`/`spawnNative` need real git worktrees + runtime registry + LLM mocks | Refactor monitor helpers to be unit-testable; integration suite for spawn paths (PR 2, PR 5) |
| `tmux` (73%) | `realRun`/`realOutput` need live tmux binary on PATH | Live-tmux job behind a build tag, runs on a dedicated CI lane (PR 7) |
| `web` (78%) | Handler error paths require breaking projStore mid-call | Targeted store-shutdown tests (PR 4) |
| `state.Project` (~46%) | Central event-router switch with many event types | Add seeding fixtures for under-covered event types (PR 6) |

## Roadmap

PRs are ordered by **lift-per-effort ratio** so the early wins are biggest. Each item names files, function counts, and an estimate.

### PR 1 — `cli`: extract command handlers, add direct tests
**Lift:** cli 66% → ~80% · **Total:** +1.0 to +1.5 · **Effort:** medium

Each Cobra command is shaped as `RunE: runFoo`. The `runFoo` functions are pure-ish business logic but currently lack direct tests because `execCmd(t, cmd, …)` (in `testenv_test.go`) routes through Cobra parsing.

Work:
- For 8–10 commands lacking direct coverage (notably `runImprove`, `runWatch`, `runMetrics`, `runDashboard` web mode, `runDirect`, `runSpec*`), add direct-call tests that bypass the Cobra layer.
- Cover the I/O-bound paths via `bytes.Buffer` for stdout capture + the existing `testenv_test.go::setupTestEnv` fixture.

Risk: Cobra wiring changes break tests. Mitigation: refactor only the test side; production `RunE` signatures unchanged.

### PR 2 — `engine.monitor`: extract testable helpers
**Lift:** engine 74% → ~80% · **Total:** +1.0 · **Effort:** medium

Functions currently 0% inside `monitor.go`:
- `executeSplitAction` (heaviest — needs DAG mutation fixture)
- `handleTechLeadEscalation`
- `pollNativeAgent` (needs runtime stub)
- `captureStoryDiff` (needs git worktree)

Approach: Pull pure-logic chunks into `monitor_helpers.go` (already exists). Inject git/runtime via interfaces. Cover the new helpers directly; integration of the full monitor.go path stays at PR 5.

### PR 3 — `cli` interfaces for store/llm, full coverage push
**Lift:** cli 80% → ~92% · **Total:** +1.0 · **Effort:** large

Define interface types (`StoreFacade`, `LLMFactory`) inside cli or wherever they currently live as concrete deps. Production code uses real types; tests inject stubs.

This is bigger than PR 1 but unblocks any future cli work.

### PR 4 — `web`: handler error paths
**Lift:** web 78% → ~85% · **Total:** +0.4 · **Effort:** small

Pattern: `s.projStore.Close()` mid-test, then call the handler. Existing `TestHandleKill_StoreErrorReturnsErrorResponse` is the template. Apply to: `handleResume`, `handleRetry`, `handleReassign`, `handleEdit`, `handleApproveRequirement`, `handleRejectRequirement`, `handleMergeStory`.

10 tests, 1 day.

### PR 5 — `engine.spawn` integration suite
**Lift:** engine 80% → ~90% · **Total:** +1.5 to +2.0 · **Effort:** large

The big one. Build a test fixture that:
- Creates a real git repo + worktree under `t.TempDir()`.
- Stubs the runtime registry with a fake "echo" runtime that writes a deterministic diff.
- Drives `executor.SpawnAll` end-to-end through `spawn` and `spawnNative`.
- Verifies STORY_STARTED + STORY_COMPLETED events land with expected payloads.

This unblocks the bulk of engine.go's uncovered statements.

### PR 6 — small packages to 95%
**Lift:** state/runtime/git/criteria/scratchboard/dashboard/codegraph/agent each 81–87% → 95% · **Total:** +1.0 · **Effort:** medium-spread

Each package needs 3–8 targeted tests for the residual gaps. Most gaps are:
- `state.Project` event-type cases not seeded by existing tests.
- `git.CreatePR` (needs gh CLI mock).
- `runtime.gemma` self-correction loop edge cases (rejection budget exhausted).
- `criteria` evaluator unknown-type fallback.

Do them as one PR (per package commit) so the review batch stays small.

### PR 7 — live-tmux CI lane
**Lift:** tmux 73% → ~95% · **Total:** +0.3 · **Effort:** small

Add a `live_tmux_test.go` with `//go:build live_tmux` build tag. New CI job `tmux-integration` installs tmux, runs `go test -tags live_tmux ./internal/tmux/...`. Allows realRun/realOutput coverage without affecting the default test lane.

### PR 8 — `state.Project` event-type fixtures
**Lift:** state 82% → ~95% · **Total:** +0.5 · **Effort:** small

The Project method is at ~46% per-function coverage because many `case Event…:` branches have no fixture. Build a single test that sequentially emits one event of every type and asserts the projection updates correctly. ~30 events to seed.

### PR 9 — `web.Start` lifecycle
**Lift:** web 85% → ~92% · **Total:** +0.4 · **Effort:** small

`Start` is at 61% — the listener-bind + graceful-shutdown branches lack tests. Use `httptest.NewServer` plus a context-cancel signal to drive the lifecycle.

### PR 10 — final sweep + measurement
**Lift:** total → ≥95% · **Effort:** small

After PRs 1–9: re-measure. Identify any leftover gaps. Decide if any low-coverage paths are genuinely untestable (e.g., `os.Exit` calls, `panic` recovery for unreachable defaults) and either:
- Refactor them out, or
- Add `// coverage: unreachable` exclusion comments and accept them.

## Cumulative projection

| After PR | Total | Notes |
|---|---|---|
| Start | 77.8% | round 2 baseline |
| PR 1 | ~79.0% | cli direct tests |
| PR 2 | ~80.0% | engine helpers extracted |
| PR 3 | ~81.0% | cli interfaces full |
| PR 4 | ~81.5% | web error paths |
| PR 5 | ~83.5% | engine spawn integration |
| PR 6 | ~84.5% | small packages to 95% |
| PR 7 | ~84.8% | live-tmux |
| PR 8 | ~85.3% | state.Project |
| PR 9 | ~85.7% | web.Start |
| PR 10 | ≥95% IF additional sweeps | residual sweep |

> **Honest note:** the cumulative projection lands at ~86%, not 95%. Hitting 95% additionally requires either:
> - More aggressive refactor of monitor.go's spawn path (PR 5 push to ~95% engine instead of ~90%), or
> - Refactoring cli wiring for full DI (PR 3 push to ~98% cli instead of ~92%), or
> - Accepting some integration-test-only paths as covered by `//go:build integration` lanes.

A realistic 95% total **probably needs 12–15 PRs** spread over 4–8 weeks, depending on review pace. This document is the contract for that work.

## Tracking

One GitHub issue per PR; close when the corresponding PR merges. Update the cumulative-projection table as numbers come in.

| PR | Issue | Status |
|---|---|---|
| 1 — cli extract handlers | [#24](https://github.com/tzone85/nexus-dispatch/issues/24) | open |
| 2 — engine.monitor helpers | [#25](https://github.com/tzone85/nexus-dispatch/issues/25) | open |
| 3 — cli interfaces | [#26](https://github.com/tzone85/nexus-dispatch/issues/26) | open |
| 4 — web handler error paths | [#27](https://github.com/tzone85/nexus-dispatch/issues/27) | open |
| 5 — engine.spawn integration | [#28](https://github.com/tzone85/nexus-dispatch/issues/28) | open |
| 6 — small packages to 95% | [#29](https://github.com/tzone85/nexus-dispatch/issues/29) | open |
| 7 — live-tmux CI lane | [#30](https://github.com/tzone85/nexus-dispatch/issues/30) | open |
| 8 — state.Project fixtures | [#31](https://github.com/tzone85/nexus-dispatch/issues/31) | open |
| 9 — web.Start lifecycle | [#32](https://github.com/tzone85/nexus-dispatch/issues/32) | open |
| 10 — residual sweep | [#33](https://github.com/tzone85/nexus-dispatch/issues/33) | open |

## Out of scope

- Adding tests for code paths NXD has decided not to cover (e.g., `os.Exit` after `log.Fatal`, panic recovery for unreachable `default:` cases). Those go through coverage-exclusion comments, not test additions.
- Test refactors that don't lift coverage (DRY-ing helpers, etc.) — separate PR if desired.
