package test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestDryRun_PlannerPipeline exercises the DryRunClient through the full
// planning pipeline: classify repo → plan stories → verify events.
// This runs in the normal test suite (no e2e build tag required).
func TestDryRun_PlannerPipeline(t *testing.T) {
	dir := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(dir)

	es, err := state.NewFileStore(filepath.Join(resolved, "events.jsonl"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	defer es.Close()

	ps, err := state.NewSQLiteStore(filepath.Join(resolved, "proj.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer ps.Close()

	// Create a minimal project directory (greenfield).
	projectDir := filepath.Join(resolved, "project")
	os.MkdirAll(projectDir, 0o755)

	client := llm.NewDryRunClient(0)
	cfg := config.DefaultConfig()
	reqID := "r-dryrun-001"

	planner := engine.NewPlanner(client, cfg, es, ps)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := planner.Plan(ctx, reqID, "Build a REST API with CRUD endpoints and tests", projectDir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// DryRunClient always returns 3 stories.
	if len(result.Stories) != 3 {
		t.Fatalf("expected 3 stories, got %d", len(result.Stories))
	}

	// Verify stories have expected structure.
	for _, story := range result.Stories {
		if story.ID == "" {
			t.Error("story ID should not be empty")
		}
		if story.Title == "" {
			t.Error("story title should not be empty")
		}
		if story.Complexity <= 0 {
			t.Errorf("story %s complexity should be > 0, got %d", story.ID, story.Complexity)
		}
	}

	// Verify requirement was created in projection.
	req, err := ps.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("GetRequirement: %v", err)
	}
	if req.Title != "Build a REST API with CRUD endpoints and tests" {
		t.Errorf("requirement title = %q", req.Title)
	}

	// Verify stories were created in projection.
	stories, err := ps.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		t.Fatalf("ListStories: %v", err)
	}
	if len(stories) != 3 {
		t.Fatalf("expected 3 stories in projection, got %d", len(stories))
	}

	// Verify events were emitted.
	allEvents, _ := es.List(state.EventFilter{})
	if len(allEvents) < 4 { // at least: REQ_SUBMITTED + 3x STORY_CREATED
		t.Errorf("expected at least 4 events, got %d", len(allEvents))
	}

	// Verify DAG was built correctly.
	if result.Graph == nil {
		t.Fatal("expected non-nil DAG")
	}
	export := result.Graph.Export()
	if len(export.Nodes) != 3 {
		t.Errorf("DAG nodes = %d, want 3", len(export.Nodes))
	}

	// Verify the DryRunClient was actually called.
	if client.CallCount() < 1 {
		t.Error("expected at least 1 LLM call")
	}

	t.Logf("Dry-run pipeline completed: %d stories, %d events, %d LLM calls",
		len(result.Stories), len(allEvents), client.CallCount())
}

// TestDryRun_DispatchWave verifies that dispatched stories follow
// dependency ordering using DryRunClient-generated stories.
func TestDryRun_DispatchWave(t *testing.T) {
	dir := t.TempDir()
	resolved, _ := filepath.EvalSymlinks(dir)

	es, _ := state.NewFileStore(filepath.Join(resolved, "events.jsonl"))
	defer es.Close()
	ps, _ := state.NewSQLiteStore(filepath.Join(resolved, "proj.db"))
	defer ps.Close()

	projectDir := filepath.Join(resolved, "project")
	os.MkdirAll(projectDir, 0o755)

	client := llm.NewDryRunClient(0)
	cfg := config.DefaultConfig()
	reqID := "r-dryrun-002"

	planner := engine.NewPlanner(client, cfg, es, ps)
	result, err := planner.Plan(context.Background(), reqID, "Add user authentication", projectDir)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Wave 1: the first story (sequential scaffold, no deps) should be dispatched alone.
	dispatcher := engine.NewDispatcher(cfg, es, ps)
	wave1, err := dispatcher.DispatchWave(result.Graph, map[string]bool{}, reqID, result.Stories, 1)
	if err != nil {
		t.Fatalf("DispatchWave 1: %v", err)
	}

	// Sequential-first: only 1 story dispatched in wave 1.
	if len(wave1) != 1 {
		t.Fatalf("wave 1: expected 1 assignment (sequential first), got %d", len(wave1))
	}
	firstStory := wave1[0].StoryID

	// Wave 2: after first story completes, its dependents become ready.
	completed := map[string]bool{firstStory: true}
	wave2, err := dispatcher.DispatchWave(result.Graph, completed, reqID, result.Stories, 2)
	if err != nil {
		t.Fatalf("DispatchWave 2: %v", err)
	}
	if len(wave2) == 0 {
		t.Fatal("wave 2: expected at least 1 assignment after first story completes")
	}
	secondStory := wave2[0].StoryID
	if secondStory == firstStory {
		t.Error("wave 2 should dispatch a different story than wave 1")
	}

	t.Logf("Dispatch ordering verified: wave1=%s, wave2=%s",
		firstStory, secondStory)
}
