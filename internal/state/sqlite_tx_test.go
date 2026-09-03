package state_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func openTxTestStores(t *testing.T) (*state.FileStore, *state.SQLiteStore) {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"), state.WithFsync(false))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ps, err := state.NewSQLiteStore(filepath.Join(dir, "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { es.Close(); ps.Close() })
	return es, ps
}

func seedReqs(t *testing.T, es *state.FileStore, ps *state.SQLiteStore, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
			"id": fmt.Sprintf("r-%03d", i), "title": "t", "description": "d",
		})
		if err := es.Append(evt); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := ps.Project(evt); err != nil {
			t.Fatalf("Project: %v", err)
		}
	}
}

// Defect 4(b): RebuildFrom used to truncate in one transaction and replay
// event-by-event in others, so a concurrent reader could observe an empty or
// half-filled projection. Readers must only ever see the full set.
func TestSQLiteStore_RebuildFrom_IsAtomicForReaders(t *testing.T) {
	es, ps := openTxTestStores(t)
	const n = 60
	seedReqs(t, es, ps, n)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var mu sync.Mutex
	var partial []int

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			reqs, err := ps.ListRequirements()
			if err != nil {
				continue
			}
			if len(reqs) != n {
				mu.Lock()
				partial = append(partial, len(reqs))
				mu.Unlock()
			}
		}
	}()

	for i := 0; i < 5; i++ {
		if err := ps.RebuildFrom(context.Background(), es); err != nil {
			t.Fatalf("RebuildFrom: %v", err)
		}
	}
	close(stop)
	wg.Wait()

	if len(partial) > 0 {
		t.Fatalf("readers observed a partial projection during rebuild: %v", partial)
	}
	applied, _ := ps.AppliedEventCount()
	if applied != n {
		t.Errorf("watermark = %d, want %d", applied, n)
	}
}

// Defect 4(a): Project bumps the watermark in the same transaction as the
// handler. Concurrent Project + List must be race-free and end with the
// watermark equal to the number of successfully projected events.
func TestSQLiteStore_ConcurrentProjectAndList(t *testing.T) {
	_, ps := openTxTestStores(t)
	const writers, perWriter = 4, 25

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
					"id": fmt.Sprintf("r-%d-%d", w, i), "title": "t", "description": "d",
				})
				if err := ps.Project(evt); err != nil {
					t.Errorf("Project: %v", err)
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := ps.ListRequirements(); err != nil {
				t.Errorf("ListRequirements: %v", err)
			}
		}
	}()
	wg.Wait()

	reqs, _ := ps.ListRequirements()
	applied, _ := ps.AppliedEventCount()
	if len(reqs) != writers*perWriter || applied != writers*perWriter {
		t.Errorf("rows=%d watermark=%d, want both %d", len(reqs), applied, writers*perWriter)
	}
}

// A handler failure rolls back without touching the watermark.
func TestSQLiteStore_Project_FailureLeavesWatermark(t *testing.T) {
	_, ps := openTxTestStores(t)
	evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id": "dup", "title": "t", "description": "d",
	})
	if err := ps.Project(evt); err != nil {
		t.Fatalf("first Project: %v", err)
	}
	if err := ps.Project(evt); err == nil {
		t.Fatal("duplicate REQ_SUBMITTED should violate the primary key")
	}
	applied, _ := ps.AppliedEventCount()
	if applied != 1 {
		t.Errorf("watermark = %d after failed Project, want 1", applied)
	}
}

// RebuildFrom with a corrupt log leaves the existing projection intact.
func TestSQLiteStore_RebuildFrom_ListErrorKeepsProjection(t *testing.T) {
	es, ps := openTxTestStores(t)
	seedReqs(t, es, ps, 3)

	bad := &failingListStore{err: fmt.Errorf("boom")}
	if err := ps.RebuildFrom(context.Background(), bad); err == nil {
		t.Fatal("expected error from failing event store")
	}
	reqs, _ := ps.ListRequirements()
	if len(reqs) != 3 {
		t.Errorf("projection should be untouched after failed rebuild, got %d rows", len(reqs))
	}
}

type failingListStore struct{ err error }

func (f *failingListStore) Append(state.Event) error                      { return nil }
func (f *failingListStore) List(state.EventFilter) ([]state.Event, error) { return nil, f.err }
func (f *failingListStore) Count(state.EventFilter) (int, error)          { return 0, f.err }
func (f *failingListStore) Close() error                                  { return nil }

// A cancelled context aborts the replay and rolls back.
func TestSQLiteStore_RebuildFrom_CancelledContextRollsBack(t *testing.T) {
	es, ps := openTxTestStores(t)
	seedReqs(t, es, ps, 3)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ps.RebuildFrom(ctx, es); err == nil {
		t.Fatal("expected context error")
	}
	reqs, _ := ps.ListRequirements()
	if len(reqs) != 3 {
		t.Errorf("projection should be untouched after cancelled rebuild, got %d rows", len(reqs))
	}
}
