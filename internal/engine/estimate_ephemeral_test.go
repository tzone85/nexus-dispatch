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

// TestPlanner_PlanEphemeral_DoesNotPersist verifies the read-only planning path
// used by `nxd estimate`: it returns decomposed stories but writes nothing to
// the event store or projection, so it can run any number of times without
// colliding on stories.id.
func TestPlanner_PlanEphemeral_DoesNotPersist(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	eventStore, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer eventStore.Close()

	projStore, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create proj store: %v", err)
	}
	defer projStore.Close()

	resp := `[
		{"id": "s-001", "title": "A", "description": "d", "acceptance_criteria": "ac", "complexity": 3, "depends_on": []},
		{"id": "s-002", "title": "B", "description": "d", "acceptance_criteria": "ac", "complexity": 5, "depends_on": ["s-001"]}
	]`
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: resp},
		llm.CompletionResponse{Content: resp},
	)
	planner := engine.NewPlanner(client, config.DefaultConfig(), eventStore, projStore)

	for i := 0; i < 2; i++ {
		result, err := planner.PlanEphemeral(context.Background(), "est-2026", "build a thing", dir)
		if err != nil {
			t.Fatalf("ephemeral plan run %d: %v", i, err)
		}
		if len(result.Stories) != 2 {
			t.Fatalf("run %d: expected 2 stories, got %d", i, len(result.Stories))
		}
	}

	events, err := eventStore.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("ephemeral plan persisted %d events, want 0", len(events))
	}
	stories, err := projStore.ListStories(state.StoryFilter{})
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	if len(stories) != 0 {
		t.Fatalf("ephemeral plan persisted %d stories, want 0", len(stories))
	}
}

// TestEstimator_MultipleEstimates_NoCollision reproduces the reported crash:
// running `nxd estimate` twice failed with "UNIQUE constraint failed:
// stories.id" because estimation persisted planner stories under the constant
// "est-2026" prefix.
func TestEstimator_MultipleEstimates_NoCollision(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test"), 0644)

	eventStore, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer eventStore.Close()

	projStore, err := state.NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("create proj store: %v", err)
	}
	defer projStore.Close()

	resp := `[
		{"id": "s-001", "title": "Build backend", "description": "API", "acceptance_criteria": "works", "complexity": 5, "depends_on": []},
		{"id": "s-002", "title": "Build frontend", "description": "UI", "acceptance_criteria": "works", "complexity": 3, "depends_on": ["s-001"]}
	]`
	client := llm.NewReplayClient(
		llm.CompletionResponse{Content: resp},
		llm.CompletionResponse{Content: resp},
	)

	estimator := engine.NewEstimator(client, config.DefaultConfig(), eventStore, projStore)

	if _, err := estimator.Estimate(context.Background(), "Buidl the backend", dir, engine.EstimateOptions{}); err != nil {
		t.Fatalf("first estimate: %v", err)
	}
	if _, err := estimator.Estimate(context.Background(), "Build the backend and frontend", dir, engine.EstimateOptions{}); err != nil {
		t.Fatalf("second estimate must not collide: %v", err)
	}

	stories, err := projStore.ListStories(state.StoryFilter{})
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	if len(stories) != 0 {
		t.Fatalf("estimate persisted %d stories into the project, want 0", len(stories))
	}
}
