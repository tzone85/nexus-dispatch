package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newPlannerStores(t *testing.T, dir string) (state.EventStore, state.ProjectionStore) {
	t.Helper()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)
	eventStore, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	t.Cleanup(func() { _ = eventStore.Close() })
	projStore, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create proj store: %v", err)
	}
	t.Cleanup(func() { _ = projStore.Close() })
	return eventStore, projStore
}

// TestPlan_RejectsEmptyStoryList pins the fix for the silent "0 stories" plan:
// when the Tech-Lead LLM returns an empty array, Plan must error instead of
// emitting REQ_PLANNED with no stories (which strands the requirement forever).
func TestPlan_RejectsEmptyStoryList(t *testing.T) {
	dir := t.TempDir()
	eventStore, projStore := newPlannerStores(t, dir)

	client := llm.NewReplayClient(llm.CompletionResponse{Content: "[]"})
	cfg := config.DefaultConfig()
	planner := engine.NewPlanner(client, cfg, eventStore, projStore)

	_, err := planner.Plan(context.Background(), "r-empty", "Build nothing", dir)
	if err == nil {
		t.Fatal("expected error when the planner returns zero stories")
	}

	// No REQ_PLANNED event must have been emitted for a failed plan.
	events, lerr := eventStore.List(state.EventFilter{})
	if lerr != nil {
		t.Fatalf("list events: %v", lerr)
	}
	for _, e := range events {
		if e.Type == state.EventReqPlanned {
			t.Errorf("REQ_PLANNED should not be emitted when planning yields 0 stories")
		}
	}
}

// TestPlan_RejectsStoryWithEmptyID pins per-story boundary validation: an empty
// or fieldless story object from the LLM must be rejected, not dispatched
// against nothing.
func TestPlan_RejectsStoryWithEmptyID(t *testing.T) {
	dir := t.TempDir()
	eventStore, projStore := newPlannerStores(t, dir)

	// One well-formed story and one empty object.
	response := `[
		{"id": "s-001", "title": "Real", "description": "d", "acceptance_criteria": "ac", "complexity": 2, "owned_files": ["a.go"]},
		{}
	]`
	client := llm.NewReplayClient(llm.CompletionResponse{Content: response})
	cfg := config.DefaultConfig()
	planner := engine.NewPlanner(client, cfg, eventStore, projStore)

	if _, err := planner.Plan(context.Background(), "r-emptyid", "Has a blank story", dir); err == nil {
		t.Fatal("expected error for a story with an empty id/title")
	}
}
