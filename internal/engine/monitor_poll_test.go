package engine

import (
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// pollTestEventStore returns a fresh JSONL-backed event store the test owns
// and closes on cleanup. Independent of dispatcher_test.go's helper so this
// file compiles even when run in isolation.
func pollTestEventStore(t *testing.T) state.EventStore {
	t.Helper()
	es, err := state.NewFileStore(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	t.Cleanup(func() { es.Close() })
	return es
}

// TestNativeAgentCompleted_FalseOnEmptyStore asserts the detection helper
// returns false when no STORY_COMPLETED event exists for the given story.
// This is the "agent still running" path.
func TestNativeAgentCompleted_FalseOnEmptyStore(t *testing.T) {
	es := pollTestEventStore(t)
	if nativeAgentCompleted(es, "STORY-X") {
		t.Fatal("want false on empty store")
	}
}

// TestNativeAgentCompleted_TrueAfterEventAppended seeds a STORY_COMPLETED
// event and confirms the helper reports completion. Matches the read path
// pollNativeAgent takes inside the monitor loop.
func TestNativeAgentCompleted_TrueAfterEventAppended(t *testing.T) {
	es := pollTestEventStore(t)
	evt := state.NewEvent(state.EventStoryCompleted, "agent-1", "STORY-Y", map[string]any{"native": true})
	if err := es.Append(evt); err != nil {
		t.Fatalf("append: %v", err)
	}
	if !nativeAgentCompleted(es, "STORY-Y") {
		t.Fatal("want true after STORY_COMPLETED appended")
	}
}

// TestNativeAgentCompleted_FilteredByStoryID guards against a regression
// where the event filter ignores StoryID — a completion event for an
// unrelated story must NOT trigger completion for the queried one.
func TestNativeAgentCompleted_FilteredByStoryID(t *testing.T) {
	es := pollTestEventStore(t)
	evt := state.NewEvent(state.EventStoryCompleted, "agent-1", "STORY-OTHER", nil)
	if err := es.Append(evt); err != nil {
		t.Fatalf("append: %v", err)
	}
	if nativeAgentCompleted(es, "STORY-MINE") {
		t.Fatal("must not report completion when only an unrelated story completed")
	}
}
