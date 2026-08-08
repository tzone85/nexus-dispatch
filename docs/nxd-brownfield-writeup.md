# NXD brownfield pass — what we found, fixed, built, and refactored

**Summary (read this, skip the rest if you're busy).**
NXD is an event-sourced agent orchestrator: `events.jsonl` is the source of
truth, the SQLite projection is a disposable view. Two real defects were sitting
in that leaf — `List` with a limit returned the *oldest* N events instead of the
newest, and a projection that fell behind the log after a crash never caught
back up. Both are fixed and tested. On top of the fixes we added a read-only
projection-drift check to `nxd doctor`, unified the Monitor's tangled
dependency wiring behind one mechanism, and wrote a measured performance
baseline for the hottest read path. Nothing here changes external behaviour
except the two bug fixes and the one new diagnostic; the whole suite stays green.

## What we found

NXD's entry point is `cmd/nxd`; the CLI lives in `internal/cli` (cobra
commands registered in `rootCmd`), and the orchestration engine in
`internal/engine` runs the plan → dispatch → spawn → monitor → merge pipeline.
The leaf everything rests on is `internal/state`: an append-only `FileStore`
over `events.jsonl` plus a `SQLiteStore` projection. The log is authoritative;
the projection is a function of the log and can be rebuilt from it. That last
sentence is the whole design, and both defects were violations of it.

The hot read path is `FileStore.readAndFilter`, reached through `List` and
`Count`. It reopens the file, scans every line, and `json.Unmarshal`s each one
on every call — and that call fires on the monitor poll tick, the web snapshot,
and the dashboard feed. The highest-coupling type is `engine.Monitor`: one
struct carrying the whole post-execution pipeline, with a long tail of optional
dependencies wired in after construction.

Two defects, both in the state leaf:

| Defect | Symptom | Root cause |
|--------|---------|------------|
| `List` limit returned oldest N | "recent events" views showed the *first* N events ever written, not the last N | `readAndFilter` broke out of the scan once it had collected `Limit` events, truncating from the front |
| Projection never recovered from desync | after a `Project` failed mid-run, the SQLite view stayed permanently behind the log; resume could re-dispatch an already-merged story | nothing compared the projection's progress against the log on open, so the disposable view was trusted as if it were authoritative |

## What we fixed

Both defects were fixed on branch `crux/fix-recent-events-and-projection-rebuild`
(merged into this working branch as a prerequisite, since the new diagnostic
builds on the second fix).

The **tail-semantics fix** changed `FileStore.readAndFilter` to collect all
matching events and then keep the last N in chronological order, so `Limit`
returns the tail of the log. Covered by `TestFileStore_Limit_ReturnsMostRecent`
alongside the existing `TestFileStore_Limit`.

The **projection-recovery fix** added a reconciliation watermark
(`projection_meta.applied_event_count`), bumped on each successful
`SQLiteStore.Project`, plus `SQLiteStore.RebuildFrom` to truncate and replay
the projection from the log. `loadStores` now calls `rebuildProjectionIfBehind`
on open, rebuilding whenever the watermark trails the log length. The archive
command reconciles by direct write rather than by projecting its event, so it
calls `AckDirectWrite` to keep from looking perpetually behind. Covered by
`TestRebuildFrom_RecoversDesyncedProjection`.

## What we built

`nxd doctor` gained a **Projection drift** check (`checkProjectionDrift`). It
compares the projection watermark (`SQLiteStore.AppliedEventCount`) against the
event-log length (`FileStore.Count`) and warns when the projection is behind,
naming the gap:

```
⚠ Projection drift          projection is 5 event(s) behind the log (applied 1 of 6); it rebuilds automatically on the next command
```

The check is deliberately read-only: it opens only stores that already exist,
so it never materialises empty ones as a side effect, and it never triggers a
rebuild — so it reports the drift a user would actually hit *before* the next
command auto-heals it. That is the operator-visible surface of the
projection-recovery fix: the fix silently repairs drift, and this check makes
the drift observable before it disappears.

Built test-first. The drift, in-sync, and no-stores paths each have a unit test
(`TestCheckProjectionDrift_ReportsDrift`, `TestCheckProjectionDrift_InSync`,
`TestCheckProjectionDrift_NoStoresIsOK`), and a wiring test
(`TestDoctorCmd_RunsProjectionDriftCheck`) drives the real `nxd doctor` command
against a seeded drifted store and asserts the drift line appears in its output
— proving the check is switched on, not merely implemented.

## The refactor

The target was `engine.Monitor`'s dependency wiring. It carried two parallel
mechanisms for the same job: thirteen `Set*` methods that assigned optional
fields directly, and a partial set of `WithMon*` functional options in
`options.go` that assigned the same fields. Three dependencies (security gate,
doc generator, completion gate) had a `Set*` method but no option at all, so
`resume.go` wired the Monitor with a mix of both styles.

We added the three missing options and rewrote all thirteen `Set*` methods as
thin shims that delegate to `Configure(WithMon*)`. Now each optional field has
one authoritative assignment site — its option — and the two mechanisms can no
longer drift apart. Behaviour is unchanged; the production diff is 52
insertions and 21 deletions across `monitor.go` and `options.go`.
`TestMonitorSetters_DelegateToOptions` is the seam: for every dependency it
wires one Monitor via the setter and one via the option and asserts identical
field state, so a future edit that makes a setter touch a different field fails
loudly.

We deliberately did **not** try to decompose the 2000-line Monitor. That would
have been a behaviour-changing rewrite, not a bounded refactor.

## The performance baseline

Full numbers, commands, and the value/effort ranking live in the State
performance baseline (`docs/perf-baseline-state.md`) — not restated here. The
headline: `List`/`Count` cost is linear in the whole log, and a `Limit=50` tail
read pays the same scan as a full read (the limit bounds the returned slice,
not the work). The benchmarks are in `filestore_bench_test.go`.

## Corrections

Things the going-in investigation got slightly wrong, corrected on closer look.

- **The functional-options migration was already half-built.** The brief framed
  the refactor as "extract the 13 `Set*` setters into functional options" as if
  from scratch. In fact `options.go` already had eleven `WithMon*` options and a
  `Configure` method, with a comment declaring options the preferred style and
  the setters "thin shims" — except the setters were not actually shims yet.
  The work was to *finish* an abandoned migration, not start one.
- **The existing `events` command did not depend on the tail fix.** The item-6
  brief suggested an `events tail` command that would be "now correct thanks to
  the List tail fix". But `nxd events` already reads the whole log and does its
  own newest-first reverse-and-truncate in `runEvents`, so it was already
  correct. The tail fix's real beneficiaries are the web snapshot, the dashboard
  feed, and the Hub delta push, which call `List` with a `Limit` directly. That
  is why we built the projection-drift check instead of `events tail` — it is a
  genuinely new capability that does not overlap `events` or `watch`.
- **The two fixes were on a separate branch, not on `main`.** We branched from
  `main` as instructed, then merged `crux/fix-recent-events-and-projection-rebuild`
  in as a prerequisite so the new diagnostic could build on the recovery fix.
  Worth stating plainly rather than pretending the fixes were already on `main`.

## Reproduce it yourself

Run from the repo root on branch `crux/brownfield-perf-feature-refactor`.

```
# Whole suite stays green (race on the packages we touched).
go test ./... -count=1
go test ./internal/state/ ./internal/engine/ ./internal/cli/ -race -count=1

# The two fixes' tests.
go test ./internal/state/ -run 'TestFileStore_Limit_ReturnsMostRecent|TestRebuildFrom_RecoversDesyncedProjection' -v -count=1
# expect: PASS for both

# The refactor seam test.
go test ./internal/engine/ -run TestMonitorSetters_DelegateToOptions -v -count=1
# expect: PASS with one subtest per optional dependency

# The new feature — unit + wiring.
go test ./internal/cli/ -run 'ProjectionDrift|RunsProjectionDriftCheck' -v -count=1
# expect: PASS; the wiring test asserts the drift line in real doctor output
```

To see the drift check fire against a real workspace: run any command that
appends events, kill it mid-run so a `Project` is skipped (or hand-append a line
to `events.jsonl`), then run `nxd doctor` before any other command — the
Projection drift line reports how far behind the projection is. Run a normal
command first and it auto-heals, and `doctor` then reports "in sync".

## How to measure

The performance numbers are reproduced by the benchmark commands in the State
performance baseline (`docs/perf-baseline-state.md`), each figure printed beside
the exact `go test -bench` invocation that produced it. The short version:

```
go test ./internal/state/ -run '^$' -bench 'BenchmarkFileStore_' -benchmem -benchtime=50x -count=1
```

Pin the machine (the recorded numbers are Apple M4 Pro); the portable findings
are the linear scaling with log length and the roughly ten allocations per
event, not the absolute milliseconds.
