package state_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Tests for filestore features not covered by filestore_test.go:
// - After time filter
// - OnAppend callback
// - Empty list

func TestFileStore_ListFilterByAfter(t *testing.T) {
	dir := t.TempDir()
	fs, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer fs.Close()

	fs.Append(state.NewEvent(state.EventStoryProgress, "agent", "s-001", nil))
	cutoff := time.Now()
	time.Sleep(10 * time.Millisecond)
	fs.Append(state.NewEvent(state.EventStoryProgress, "agent", "s-002", nil))

	events, _ := fs.List(state.EventFilter{After: cutoff})
	if len(events) != 1 {
		t.Fatalf("expected 1 event after cutoff, got %d", len(events))
	}
}

func TestFileStore_OnAppendCallback(t *testing.T) {
	dir := t.TempDir()
	fs, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer fs.Close()

	var callbackCount int
	fs.OnAppend = func(evt state.Event) {
		callbackCount++
	}

	fs.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	fs.Append(state.NewEvent(state.EventStoryCreated, "tl", "s-001", nil))

	if callbackCount != 2 {
		t.Errorf("callback count = %d, want 2", callbackCount)
	}
}

func TestFileStore_EmptyList(t *testing.T) {
	dir := t.TempDir()
	fs, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer fs.Close()

	events, err := fs.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("expected 0 events, got %d", len(events))
	}
}
