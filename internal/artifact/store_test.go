package artifact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStore_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	lc := LaunchConfig{
		StoryID: "s-001",
		Runtime: "gemma",
		Model:   "gemma4:e4b",
		Prompt:  "implement foo",
	}

	if err := store.Write("s-001", TypeLaunchConfig, lc); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := store.Read("s-001", "launch_config.json")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	var got LaunchConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Model != "gemma4:e4b" {
		t.Errorf("model = %q, want gemma4:e4b", got.Model)
	}
}

func TestStore_WriteRaw(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	if err := store.WriteRaw("s-002", TypeGitDiff, "diff --git a/main.go\n+hello\n"); err != nil {
		t.Fatalf("WriteRaw: %v", err)
	}

	data, err := store.Read("s-002", "git_diff.patch")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(data) != "diff --git a/main.go\n+hello\n" {
		t.Errorf("content = %q", string(data))
	}
}

func TestStore_Append(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	for i := 0; i < 3; i++ {
		if err := store.Append("s-003", TypeTraceEvents, map[string]int{"iter": i}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	data, err := os.ReadFile(filepath.Join(dir, "s-003", "trace_events.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 3 {
		t.Errorf("lines = %d, want 3", lines)
	}
}

func TestStore_List(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	store.Write("s-004", TypeLaunchConfig, map[string]string{"a": "b"})
	store.WriteRaw("s-004", TypeGitDiff, "diff")

	names, err := store.List("s-004")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("len = %d, want 2; got %v", len(names), names)
	}
}

func TestStore_ListEmpty(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	names, err := store.List("nonexistent")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

func TestStore_ConcurrentAppend(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewStore(dir)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			store.Append("s-005", TypeTraceEvents, map[string]int{"n": n})
		}(i)
	}
	wg.Wait()

	data, _ := os.ReadFile(filepath.Join(dir, "s-005", "trace_events.jsonl"))
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines != 10 {
		t.Errorf("lines = %d, want 10", lines)
	}
}
