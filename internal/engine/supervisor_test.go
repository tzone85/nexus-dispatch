package engine_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestSupervisor_Review_OnTrack(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: `{"on_track": true, "concerns": [], "reprioritize": []}`,
	})

	supervisor := engine.NewSupervisor(client, "sonnet", 4000, es)
	result, err := supervisor.Review(
		context.Background(),
		"Add auth",
		[]engine.PlannedStory{{ID: "s-001", Title: "Add user model", Complexity: 3}},
		map[string]string{"s-001": "in_progress"},
	)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if !result.OnTrack {
		t.Fatal("expected on track")
	}

	// Verify SUPERVISOR_CHECK event emitted (not drift)
	checkEvents, err := es.List(state.EventFilter{Type: state.EventSupervisorCheck})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(checkEvents) != 1 {
		t.Fatalf("expected 1 SUPERVISOR_CHECK event, got %d", len(checkEvents))
	}
}

func TestSupervisor_Review_DriftDetected(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: `{"on_track": false, "concerns": ["Story s-002 is not relevant to auth"], "reprioritize": ["s-002"]}`,
	})

	supervisor := engine.NewSupervisor(client, "sonnet", 4000, es)
	result, err := supervisor.Review(
		context.Background(),
		"Add auth",
		[]engine.PlannedStory{
			{ID: "s-001", Title: "User model", Complexity: 3},
			{ID: "s-002", Title: "Unrelated task", Complexity: 5},
		},
		map[string]string{"s-001": "merged", "s-002": "in_progress"},
	)
	if err != nil {
		t.Fatalf("review: %v", err)
	}
	if result.OnTrack {
		t.Fatal("expected drift detected")
	}
	if len(result.Concerns) != 1 {
		t.Fatalf("expected 1 concern, got %d", len(result.Concerns))
	}
	if len(result.Reprioritize) != 1 {
		t.Fatalf("expected 1 reprioritize, got %d", len(result.Reprioritize))
	}

	// Verify drift event emitted
	events, err := es.List(state.EventFilter{Type: state.EventSupervisorDriftDetected})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 drift event, got %d", len(events))
	}
}

func TestSupervisor_Review_LLMError(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	client := llm.NewReplayClient() // no responses
	supervisor := engine.NewSupervisor(client, "sonnet", 4000, es)

	_, err = supervisor.Review(
		context.Background(),
		"Add auth",
		[]engine.PlannedStory{{ID: "s-001", Title: "Task", Complexity: 3}},
		map[string]string{},
	)
	if err == nil {
		t.Fatal("expected LLM error")
	}
}

func TestSupervisor_Review_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer es.Close()

	client := llm.NewReplayClient(llm.CompletionResponse{Content: "not json"})
	supervisor := engine.NewSupervisor(client, "sonnet", 4000, es)

	_, err = supervisor.Review(
		context.Background(),
		"Add auth",
		[]engine.PlannedStory{{ID: "s-001", Title: "Task", Complexity: 3}},
		map[string]string{},
	)
	if err == nil {
		t.Fatal("expected parse error")
	}
}
