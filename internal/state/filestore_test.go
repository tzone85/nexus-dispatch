package state_test

import (
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestFileStore_AppendAndList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	store, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	defer store.Close()

	evt1 := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{"title": "Add auth"})
	evt2 := state.NewEvent(state.EventStoryCreated, "tech-lead", "s-001", map[string]any{"title": "OAuth middleware"})

	if err := store.Append(evt1); err != nil {
		t.Fatalf("append evt1: %v", err)
	}
	if err := store.Append(evt2); err != nil {
		t.Fatalf("append evt2: %v", err)
	}

	events, err := store.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != state.EventReqSubmitted {
		t.Fatalf("expected REQ_SUBMITTED, got %s", events[0].Type)
	}
	if events[1].Type != state.EventStoryCreated {
		t.Fatalf("expected STORY_CREATED, got %s", events[1].Type)
	}
}

func TestFileStore_FilterByType(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	store.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store.Append(state.NewEvent(state.EventStoryCreated, "tl", "s-1", nil))
	store.Append(state.NewEvent(state.EventStoryCreated, "tl", "s-2", nil))

	events, _ := store.List(state.EventFilter{Type: state.EventStoryCreated})
	if len(events) != 2 {
		t.Fatalf("expected 2 story events, got %d", len(events))
	}
}

func TestFileStore_FilterByStoryID(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	store.Append(state.NewEvent(state.EventStoryStarted, "jr-1", "s-1", nil))
	store.Append(state.NewEvent(state.EventStoryStarted, "jr-2", "s-2", nil))
	store.Append(state.NewEvent(state.EventStoryCompleted, "jr-1", "s-1", nil))

	events, _ := store.List(state.EventFilter{StoryID: "s-1"})
	if len(events) != 2 {
		t.Fatalf("expected 2 events for s-1, got %d", len(events))
	}
}

func TestFileStore_FilterByAgentID(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	store.Append(state.NewEvent(state.EventStoryStarted, "jr-1", "s-1", nil))
	store.Append(state.NewEvent(state.EventStoryStarted, "jr-2", "s-2", nil))

	events, _ := store.List(state.EventFilter{AgentID: "jr-1"})
	if len(events) != 1 {
		t.Fatalf("expected 1 event for jr-1, got %d", len(events))
	}
}

func TestFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	store1, _ := state.NewFileStore(path)
	store1.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store1.Close()

	store2, _ := state.NewFileStore(path)
	defer store2.Close()
	events, _ := store2.List(state.EventFilter{})
	if len(events) != 1 {
		t.Fatalf("expected 1 event after reopen, got %d", len(events))
	}
}

func TestFileStore_Count(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	store.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store.Append(state.NewEvent(state.EventStoryCreated, "tl", "s-1", nil))

	count, _ := store.Count(state.EventFilter{})
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}

	count, _ = store.Count(state.EventFilter{Type: state.EventReqSubmitted})
	if count != 1 {
		t.Fatalf("expected count 1, got %d", count)
	}
}

func TestFileStore_Limit(t *testing.T) {
	dir := t.TempDir()
	store, _ := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	defer store.Close()

	for i := 0; i < 10; i++ {
		store.Append(state.NewEvent(state.EventStoryProgress, "jr-1", "s-1", nil))
	}

	events, _ := store.List(state.EventFilter{Limit: 3})
	if len(events) != 3 {
		t.Fatalf("expected 3 events with limit, got %d", len(events))
	}
}
