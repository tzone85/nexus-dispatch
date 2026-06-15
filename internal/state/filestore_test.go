package state_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// F7: a half-written event line in events.jsonl previously short-
// circuited as `continue`, silently dropping the corrupt record. Because
// the projection store, retry counter, metrics aggregator, and resume
// logic all re-derive truth from this file, that silent skip let the
// rest of NXD run on a degraded view of state. Default behaviour is now
// to surface the corruption with a line number.
func TestFileStore_List_SurfacesCorruptLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	store, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	store.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store.Close()

	// Append a half-written line that json.Unmarshal will reject.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{not json\n")
	f.Close()

	store2, err := state.NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()
	_, err = store2.List(state.EventFilter{})
	if err == nil {
		t.Fatal("expected error on corrupt line, got nil")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q should cite line 2", err)
	}
}

// Lenient mode preserves the legacy silent-skip behaviour for emergency
// recovery.
func TestFileStore_List_LenientSkipsCorruption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	store, _ := state.NewFileStore(path)
	store.Append(state.NewEvent(state.EventReqSubmitted, "system", "", nil))
	store.Close()

	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("{garbage\n")
	f.Close()

	t.Setenv("NXD_EVENTS_LENIENT", "1")
	store2, _ := state.NewFileStore(path)
	defer store2.Close()
	events, err := store2.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("lenient mode should not error: %v", err)
	}
	if len(events) != 1 {
		t.Errorf("expected 1 valid event, got %d", len(events))
	}
}

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
