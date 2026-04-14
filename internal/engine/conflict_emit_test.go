package engine

import (
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestEmitResolutionEvent_WithStore(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer es.Close()

	cr := &ConflictResolver{
		eventStore: es,
	}

	cr.emitResolutionEvent("s-001", []string{"main.go", "handler.go"}, 2)

	events, err := es.List(state.EventFilter{Type: state.EventStoryProgress, StoryID: "s-001"})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	payload := state.DecodePayload(events[0].Payload)
	if payload["action"] != "conflicts_resolved" {
		t.Errorf("expected action conflicts_resolved, got %v", payload["action"])
	}
	if int(payload["rounds"].(float64)) != 2 {
		t.Errorf("expected 2 rounds, got %v", payload["rounds"])
	}
}

func TestEmitResolutionEvent_NilStore(t *testing.T) {
	cr := &ConflictResolver{
		eventStore: nil,
	}
	// Should not panic with nil event store
	cr.emitResolutionEvent("s-001", []string{"file.go"}, 1)
}
