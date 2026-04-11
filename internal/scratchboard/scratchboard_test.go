package scratchboard

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestWriteAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.jsonl")
	sb, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sb.Write(Entry{AgentID: "a1", StoryID: "s-001", Category: "pattern", Content: "use sync.Mutex for store"})
	sb.Write(Entry{AgentID: "a2", StoryID: "s-002", Category: "gotcha", Content: "go.mod requires go 1.22"})

	entries, err := sb.Read("", 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2", len(entries))
	}
	// Newest first.
	if entries[0].Category != "gotcha" {
		t.Errorf("entries[0].Category = %q, want gotcha", entries[0].Category)
	}
}

func TestReadByCategory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.jsonl")
	sb, _ := New(path)

	sb.Write(Entry{Category: "pattern", Content: "a"})
	sb.Write(Entry{Category: "gotcha", Content: "b"})
	sb.Write(Entry{Category: "pattern", Content: "c"})

	entries, _ := sb.Read("pattern", 10)
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2", len(entries))
	}
}

func TestReadLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.jsonl")
	sb, _ := New(path)

	for i := 0; i < 30; i++ {
		sb.Write(Entry{Content: "entry"})
	}

	entries, _ := sb.Read("", 5)
	if len(entries) != 5 {
		t.Fatalf("len = %d, want 5", len(entries))
	}
}

func TestReadEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.jsonl")
	sb, _ := New(path)

	entries, err := sb.Read("", 10)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty, got %d", len(entries))
	}
}

func TestConcurrentWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.jsonl")
	sb, _ := New(path)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sb.Write(Entry{Content: "entry"})
		}(i)
	}
	wg.Wait()

	entries, _ := sb.Read("", 100)
	if len(entries) != 20 {
		t.Errorf("len = %d, want 20", len(entries))
	}
}

func TestSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.jsonl")
	sb, _ := New(path)

	sb.Write(Entry{StoryID: "s-001", Category: "pattern", Content: "use interfaces"})

	snap := sb.Snapshot(10)
	if snap == "" {
		t.Fatal("expected non-empty snapshot")
	}
	if !contains(snap, "use interfaces") {
		t.Errorf("snapshot missing content: %s", snap)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
