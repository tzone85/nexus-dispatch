# Performance baseline — the FileStore hot path

**Finding.** `FileStore.List` and `FileStore.Count` cost is linear in the whole
event log, not in what the caller asked for. Reading a 50k-event
`events.jsonl` takes roughly 80 ms and allocates ~500k objects — every call,
because `readAndFilter` reopens the file, rescans it, and `json.Unmarshal`s
every line before any filter or limit is applied. That call sits on the
monitor poll tick, the web "Last 50 events" snapshot, and the dashboard
activity feed, so the cost repeats on a timer. `Count` and a `Limit=50` tail
read pay the same scan as a full unfiltered read: the limit bounds the result
slice, not the work.

This document is a measurement, not a fix. No optimization is proposed here
that has not been measured; the ranking at the end is scoped to what the
numbers above justify.

## Environment

| Field | Value |
|-------|-------|
| goos / goarch | darwin / arm64 |
| cpu | Apple M4 Pro |
| go | go1.26.4 |
| pkg | `github.com/tzone85/nexus-dispatch/internal/state` |

Numbers are machine-specific; reproduce on your own hardware with the commands
below and compare shape (linear scaling, allocs-per-event), not absolute ms.

## What was measured

The synthetic log is deterministic (fixed RNG seed) so it regenerates
byte-for-byte: `writeSyntheticLog` in `filestore_bench_test.go` writes N events
cycling through eight event types, each with a small JSON payload, at
monotonically increasing timestamps. Log generation is excluded from every
timer via `newLoadedStore`.

Four standard benchmarks (`ns/op` mean + `allocs/op`) and one percentile
harness (`BenchmarkFileStore_ListPercentiles`, reporting p50/p95/p99 of
per-call latency) exercise the path:

| Benchmark | Call under test | Why it matters |
|-----------|-----------------|----------------|
| `BenchmarkFileStore_List` | `List(EventFilter{})` | full read — monitor tick, web snapshot |
| `BenchmarkFileStore_ListTail` | `List(EventFilter{Limit: 50})` | dashboard tail — does the limit save work? |
| `BenchmarkFileStore_ListTypeFilter` | `List(EventFilter{Type: STORY_MERGED})` | filtered read — does the filter save the scan? |
| `BenchmarkFileStore_Count` | `Count(EventFilter{})` | does Count avoid materialising the slice? |
| `BenchmarkFileStore_ListPercentiles` | `List(EventFilter{})` | tail latency the dashboard feels, not the mean |

## Warm steady-state numbers

All measurements are warm steady-state: the OS page cache is hot after the
first iteration. Cold-start (first read off cold disk) is a separate
population and is **not** measured here — flagged in the Gaps section.

### Mean throughput and allocations

`-benchtime=50x`, so each row is 50 calls.

| Operation | N | ns/op | ms/op | B/op | allocs/op |
|-----------|----:|-----------:|------:|-----------:|----------:|
| List (full) | 1,000 | 1,743,546 | 1.74 | 890,116 | 10,016 |
| List (full) | 10,000 | 15,655,493 | 15.66 | 9,454,471 | 100,023 |
| List (full) | 50,000 | 87,812,332 | 87.81 | 51,491,608 | 500,030 |
| List (Limit=50) | 1,000 | 1,937,924 | 1.94 | 890,001 | 10,016 |
| List (Limit=50) | 10,000 | 15,663,502 | 15.66 | 9,454,344 | 100,023 |
| List (Limit=50) | 50,000 | 78,181,373 | 78.18 | 51,491,518 | 500,030 |
| List (Type filter) | 1,000 | 1,467,085 | 1.47 | 529,571 | 10,012 |
| List (Type filter) | 10,000 | 15,615,202 | 15.62 | 5,325,586 | 100,016 |
| List (Type filter) | 50,000 | 84,763,948 | 84.76 | 28,045,994 | 500,022 |
| Count (full) | 1,000 | 1,585,022 | 1.59 | 890,003 | 10,016 |
| Count (full) | 10,000 | 17,791,273 | 17.79 | 9,454,352 | 100,023 |
| Count (full) | 50,000 | 79,624,868 | 79.62 | 51,491,378 | 500,030 |

Reproduce (all four standard benchmarks, one command):

```
go test ./internal/state/ -run '^$' \
  -bench 'BenchmarkFileStore_(List|ListTail|ListTypeFilter|Count)$' \
  -benchmem -benchtime=50x -count=1
```

### Per-call latency percentiles

`-benchtime=200x`, so each distribution is 200 calls. The mean (`ns/op`) is
shown beside the percentiles to make the skew visible.

| N | mean ns/op | p50 ns | p95 ns | p99 ns | p50 ms | p95 ms | p99 ms |
|----:|-----------:|--------:|---------:|---------:|------:|------:|------:|
| 1,000 | 1,539,351 | 1,494,292 | 1,783,542 | 2,264,750 | 1.49 | 1.78 | 2.26 |
| 10,000 | 16,777,470 | 15,619,584 | 22,292,458 | 39,163,000 | 15.62 | 22.29 | 39.16 |
| 50,000 | 84,031,936 | 79,539,542 | 116,849,833 | 157,281,250 | 79.54 | 116.85 | 157.28 |

Reproduce:

```
go test ./internal/state/ -run '^$' \
  -bench 'BenchmarkFileStore_ListPercentiles$' \
  -benchtime=200x -count=1
```

## Decomposition

Every millisecond attributed to a hop before any hop is blamed. The path has
no network or queueing; it is local file I/O plus CPU-bound decode.

| Hop | What it does | Evidence it dominates |
|-----|--------------|-----------------------|
| Open + scan | `os.Open` then `bufio.Scanner` over the file | Fixed per call; negligible next to decode at N≥10k |
| Decode | one `json.Unmarshal` per line into an `Event` | ~10 allocs/event tracks line count exactly (10,016 at 1k → 500,030 at 50k); B/op scales with retained events |
| Filter / limit | in-memory predicate + tail slice after decode | Type filter cuts B/op ~2x at 50k (28.0 MB vs 51.5 MB) but leaves ns/op within noise — proof the filter runs *after* the scan, saving retention not work |

Two independent signals agree the decode hop dominates: allocs/op equals
~10 per event across every operation regardless of filter (the unmarshal
allocates the same whether or not the result is kept), and ns/op is flat
across List / ListTail / Count at each N (they share the identical scan). The
`Limit=50` tail costing the same as the full read is the clearest single
number: a caller asking for 50 events out of 50,000 still waits ~80 ms.

## How to measure

Every figure above carries its command in the section that states it. To
regenerate the whole baseline in one pass:

```
go test ./internal/state/ -run '^$' \
  -bench 'BenchmarkFileStore_' -benchmem -benchtime=50x -count=1
```

For stable percentiles raise the iteration count (`-benchtime=200x` or higher);
for variance across runs add `-count=10` and feed the output to `benchstat`.
Pin the machine — these are Apple M4 Pro numbers and will differ elsewhere; the
linear scaling and the ~10 allocs/event ratio are the portable findings.

## Gaps (measured honestly)

- **Cold-start not measured.** All numbers are warm (page cache hot). First
  read off cold disk is a separate population; the percentile harness warms
  once before recording specifically to keep cold outliers out of the warm
  distribution.
- **No concurrency.** Benchmarks are single-caller. `List` takes `mu.RLock`, so
  concurrent readers do not contend, but a concurrent `Append` (write lock)
  would serialise against them — unmeasured here.
- **Second instrument source.** The skill asks for two independent evidence
  sources. Only the Go benchmark harness is wired; OTEL/span cross-check is not
  available in NXD's current build. Allocs/op vs line-count is used as the
  internal cross-check instead (they agree exactly), but that is one
  instrument, not two — noted rather than hidden.

## Fixes ranked by value over effort

Ranked from the decomposition, not from vibes. None implemented — this is a
baseline.

| Fix | Est. latency saved | Effort | Risk | Notes |
|-----|--------------------|--------|------|-------|
| Reverse-read tail for `Limit` reads (read file end-first, stop after N) | High for tail reads — turns the ~80 ms `Limit=50` at 50k into near-constant | Medium | Medium | Biggest win where it counts (dashboard/web only ever want recent N); must preserve the tail-order semantics the recent-events fix established |
| Cache `Count` / cheap length tracking | Removes full scan from every `Count` | Low | Low | `Count` today materialises the whole slice to return an int |
| Reuse a decode buffer / `Event` pool across lines | Cuts allocs/op, trims GC pressure | Low | Low | ~10 allocs/event is the alloc driver; helps every path |
| Incremental in-memory projection fed by `OnAppend` | Removes the rescan entirely for hot readers | High | High | Largest structural win and the largest blast radius; a separate design, not a tweak |

Highest value per effort first: the `Count` and buffer-reuse rows are cheap and
safe; the reverse-read tail is the highest-value targeted fix; the in-memory
projection is the structural end state but out of scope for a baseline.
