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

func TestPlanner_Plan(t *testing.T) {
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

	techLeadResponse := `[
		{"id": "s-001", "title": "Add user model", "description": "Create user struct and DB table", "acceptance_criteria": "User model exists with tests", "complexity": 3, "depends_on": []},
		{"id": "s-002", "title": "Add auth middleware", "description": "JWT validation middleware", "acceptance_criteria": "Middleware validates tokens", "complexity": 5, "depends_on": ["s-001"]},
		{"id": "s-003", "title": "Add login endpoint", "description": "POST /api/login", "acceptance_criteria": "Returns JWT on valid credentials", "complexity": 3, "depends_on": ["s-001"]}
	]`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: techLeadResponse,
		Model:   "claude-opus-4",
	})

	cfg := config.DefaultConfig()
	planner := engine.NewPlanner(client, cfg, eventStore, projStore)

	result, err := planner.Plan(context.Background(), "r-001", "Add user authentication", dir)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Verify stories
	if len(result.Stories) != 3 {
		t.Fatalf("expected 3 stories, got %d", len(result.Stories))
	}
	if result.Stories[0].Title != "Add user model" {
		t.Fatalf("expected 'Add user model', got %s", result.Stories[0].Title)
	}

	// Verify dependency graph
	waves, err := result.Graph.Waves()
	if err != nil {
		t.Fatalf("waves: %v", err)
	}
	if len(waves) != 2 {
		t.Fatalf("expected 2 waves, got %d", len(waves))
	}

	// Verify events were emitted
	events, err := eventStore.List(state.EventFilter{Type: state.EventStoryCreated})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 STORY_CREATED events, got %d", len(events))
	}

	// Verify projection (story IDs are prefixed with short reqID)
	story, err := projStore.GetStory("r-001-s-001")
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Title != "Add user model" {
		t.Fatalf("expected projected story title 'Add user model', got %s", story.Title)
	}

	// Verify LLM was called exactly once
	if client.CallCount() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", client.CallCount())
	}
}

func TestPlanner_CycleDetection(t *testing.T) {
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

	response := `[
		{"id": "s-001", "title": "A", "description": "d", "acceptance_criteria": "ac", "complexity": 3, "depends_on": ["s-002"]},
		{"id": "s-002", "title": "B", "description": "d", "acceptance_criteria": "ac", "complexity": 3, "depends_on": ["s-001"]}
	]`

	client := llm.NewReplayClient(llm.CompletionResponse{Content: response})
	planner := engine.NewPlanner(client, config.DefaultConfig(), eventStore, projStore)

	_, err = planner.Plan(context.Background(), "r-001", "test", dir)
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestPlanner_InvalidJSON(t *testing.T) {
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

	client := llm.NewReplayClient(llm.CompletionResponse{Content: "not json"})
	planner := engine.NewPlanner(client, config.DefaultConfig(), eventStore, projStore)

	_, err = planner.Plan(context.Background(), "r-001", "test", dir)
	if err == nil {
		t.Fatal("expected parse error for invalid JSON")
	}
}

func TestPlanner_LLMError(t *testing.T) {
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

	// No responses loaded -> will error
	client := llm.NewReplayClient()
	planner := engine.NewPlanner(client, config.DefaultConfig(), eventStore, projStore)

	_, err = planner.Plan(context.Background(), "r-001", "test", dir)
	if err == nil {
		t.Fatal("expected LLM error when replay client is exhausted")
	}
}
