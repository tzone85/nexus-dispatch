package state_test

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// benchSizes are the synthetic event-log lengths the hot-path benchmarks run
// against. FileStore.readAndFilter does a full linear rescan plus a
// json.Unmarshal per line on every List/Count, and List/Count sit on the
// monitor poll tick, the web "Last 50 events" snapshot, and the dashboard
// activity feed — so cost grows linearly with the log and these sizes bracket
// a young workspace (1k), a busy one (10k), and a long-lived one (50k).
var benchSizes = []int{1000, 10000, 50000}

// benchEventTypes is a representative spread so Type-filtered reads have a
// realistic hit rate (STORY_MERGED lands on roughly one line in eight).
var benchEventTypes = []state.EventType{
	state.EventReqSubmitted,
	state.EventStoryCreated,
	state.EventStoryStarted,
	state.EventStoryProgress,
	state.EventStoryReviewPassed,
	state.EventStoryQAPassed,
	state.EventStoryMerged,
	state.EventAgentCheckpoint,
}

// writeSyntheticLog fills path with n deterministic events (fixed seed) so the
// numbers reproduce byte-for-byte across runs and machines. Payloads carry a
// small map, matching the shape real NXD events unmarshal into.
func writeSyntheticLog(tb testing.TB, path string, n int) {
	tb.Helper()
	store, err := state.NewFileStore(path)
	if err != nil {
		tb.Fatalf("new file store: %v", err)
	}
	defer store.Close()

	rng := rand.New(rand.NewSource(1))
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		et := benchEventTypes[i%len(benchEventTypes)]
		evt := state.NewEvent(
			et,
			fmt.Sprintf("agent-%d", rng.Intn(16)),
			fmt.Sprintf("story-%d", rng.Intn(256)),
			map[string]any{
				"seq":    i,
				"title":  "synthetic story for benchmark",
				"status": "in_progress",
				"wave":   rng.Intn(8),
			},
		)
		// Deterministic, monotonically increasing timestamps so After-filter
		// benchmarks have a stable midpoint to cut against.
		evt.Timestamp = base.Add(time.Duration(i) * time.Second)
		if err := store.Append(evt); err != nil {
			tb.Fatalf("append event %d: %v", i, err)
		}
	}
}

// newLoadedStore writes a synthetic log of n events and returns a store open
// over it. The generation cost is excluded from the caller's benchmark timer.
func newLoadedStore(tb testing.TB, n int) *state.FileStore {
	tb.Helper()
	path := filepath.Join(tb.TempDir(), "events.jsonl")
	writeSyntheticLog(tb, path, n)
	store, err := state.NewFileStore(path)
	if err != nil {
		tb.Fatalf("reopen store: %v", err)
	}
	return store
}

// BenchmarkFileStore_List measures a full unfiltered List — the worst case,
// materialising every event. This is what the monitor tick and web snapshot
// pay when they call List with no Limit.
func BenchmarkFileStore_List(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			store := newLoadedStore(b, n)
			defer store.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				evs, err := store.List(state.EventFilter{})
				if err != nil {
					b.Fatal(err)
				}
				if len(evs) != n {
					b.Fatalf("want %d events, got %d", n, len(evs))
				}
			}
		})
	}
}

// BenchmarkFileStore_ListTail measures List with Limit=50 (the tail path the
// dashboard and web snapshot actually use). Note the whole file is still
// scanned and unmarshalled before the tail is sliced off — the Limit bounds
// the returned slice, not the work, which is exactly the finding to surface.
func BenchmarkFileStore_ListTail(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			store := newLoadedStore(b, n)
			defer store.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				evs, err := store.List(state.EventFilter{Limit: 50})
				if err != nil {
					b.Fatal(err)
				}
				if len(evs) != 50 {
					b.Fatalf("want 50 events, got %d", len(evs))
				}
			}
		})
	}
}

// BenchmarkFileStore_ListTypeFilter measures a Type-filtered List. The filter
// runs after unmarshal, so it trims the result set but not the scan cost.
func BenchmarkFileStore_ListTypeFilter(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			store := newLoadedStore(b, n)
			defer store.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := store.List(state.EventFilter{Type: state.EventStoryMerged}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFileStore_Count measures Count, which delegates to List and then
// discards the slice — so it pays the full read and unmarshal to return an int.
func BenchmarkFileStore_Count(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			store := newLoadedStore(b, n)
			defer store.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := store.Count(state.EventFilter{})
				if err != nil {
					b.Fatal(err)
				}
				if got != n {
					b.Fatalf("want count %d, got %d", n, got)
				}
			}
		})
	}
}

// BenchmarkFileStore_ListPercentiles reports the per-call latency distribution
// (p50/p95/p99) of a full List, not just the mean ns/op the standard
// benchmarks give. Percentiles matter here because the monitor tick and the
// web broadcast fire this call on a timer: p95 is the tick a user watching the
// dashboard actually feels stutter on. Warm steady-state only — the OS page
// cache is hot after the first iteration, so cold-start disk latency is a
// separate population not measured here.
func BenchmarkFileStore_ListPercentiles(b *testing.B) {
	for _, n := range benchSizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			store := newLoadedStore(b, n)
			defer store.Close()
			// Warm the page cache and the store's read path once so the
			// recorded population is steady-state, not first-hit.
			if _, err := store.List(state.EventFilter{}); err != nil {
				b.Fatal(err)
			}

			durations := make([]time.Duration, 0, b.N)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				start := time.Now()
				if _, err := store.List(state.EventFilter{}); err != nil {
					b.Fatal(err)
				}
				durations = append(durations, time.Since(start))
			}
			b.StopTimer()

			sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
			b.ReportMetric(float64(percentile(durations, 0.50).Nanoseconds()), "p50-ns")
			b.ReportMetric(float64(percentile(durations, 0.95).Nanoseconds()), "p95-ns")
			b.ReportMetric(float64(percentile(durations, 0.99).Nanoseconds()), "p99-ns")
		})
	}
}

// percentile returns the p-th percentile (0..1) of a sorted duration slice
// using nearest-rank. Returns 0 for an empty slice.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(p * float64(len(sorted)))
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}
