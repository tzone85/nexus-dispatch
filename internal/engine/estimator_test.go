package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestEstimator_LiveEstimate(t *testing.T) {
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
		{"id": "s-001", "title": "Setup OAuth middleware", "description": "Create OAuth2 middleware", "acceptance_criteria": "Middleware works", "complexity": 3, "depends_on": []},
		{"id": "s-002", "title": "Google provider", "description": "Add Google OAuth", "acceptance_criteria": "Google login works", "complexity": 3, "depends_on": ["s-001"]},
		{"id": "s-003", "title": "Session management", "description": "Handle sessions", "acceptance_criteria": "Sessions persist", "complexity": 5, "depends_on": ["s-001"]}
	]`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: techLeadResponse,
		Model:   "gemma-4-26b",
	})

	cfg := config.DefaultConfig()
	estimator := engine.NewEstimator(client, cfg, eventStore, projStore)

	est, err := estimator.Estimate(context.Background(), "Add OAuth2 login", dir, engine.EstimateOptions{})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}

	if est.IsQuick {
		t.Fatal("expected live estimate, got quick")
	}
	if est.Summary.StoryCount != 3 {
		t.Fatalf("expected 3 stories, got %d", est.Summary.StoryCount)
	}
	if est.Summary.TotalPoints != 11 {
		t.Fatalf("expected 11 points, got %d", est.Summary.TotalPoints)
	}
	if est.Summary.Rate != 150.0 {
		t.Fatalf("expected rate 150.0, got %f", est.Summary.Rate)
	}
}

func TestEstimator_QuickEstimate(t *testing.T) {
	cfg := config.DefaultConfig()
	estimator := engine.NewEstimator(nil, cfg, nil, nil)

	est, err := estimator.Estimate(context.Background(), "Add login and registration", "", engine.EstimateOptions{Quick: true})
	if err != nil {
		t.Fatalf("quick estimate: %v", err)
	}

	if !est.IsQuick {
		t.Fatal("expected quick estimate")
	}
	if est.Summary.StoryCount < 1 {
		t.Fatal("expected at least 1 story")
	}
	if est.Summary.Rate != 150.0 {
		t.Fatalf("expected default rate 150.0, got %f", est.Summary.Rate)
	}
}

func TestEstimator_RateOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	estimator := engine.NewEstimator(nil, cfg, nil, nil)

	est, err := estimator.Estimate(context.Background(), "Add feature", "", engine.EstimateOptions{
		Quick:        true,
		RateOverride: 175.0,
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}

	if est.Summary.Rate != 175.0 {
		t.Fatalf("expected rate 175.0, got %f", est.Summary.Rate)
	}
}

func TestEstimator_SavePersistsEvent(t *testing.T) {
	dir := t.TempDir()

	eventStore, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	defer eventStore.Close()

	cfg := config.DefaultConfig()
	estimator := engine.NewEstimator(nil, cfg, eventStore, nil)

	_, err = estimator.Estimate(context.Background(), "Add feature", "", engine.EstimateOptions{
		Quick:   true,
		Save:    true,
		Project: "test-project",
	})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}

	events, err := eventStore.List(state.EventFilter{Type: state.EventReqEstimated})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 REQ_ESTIMATED event, got %d", len(events))
	}

	payload := state.DecodePayload(events[0].Payload)
	if payload["project"] != "test-project" {
		t.Fatalf("expected project 'test-project', got %v", payload["project"])
	}
}

func TestEstimator_RoleAssignment(t *testing.T) {
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
		{"id": "s-001", "title": "Simple task", "description": "Easy", "acceptance_criteria": "Done", "complexity": 2, "depends_on": []},
		{"id": "s-002", "title": "Hard task", "description": "Complex", "acceptance_criteria": "Done", "complexity": 8, "depends_on": []}
	]`

	client := llm.NewReplayClient(llm.CompletionResponse{
		Content: techLeadResponse,
		Model:   "gemma-4-26b",
	})

	cfg := config.DefaultConfig()
	// Allow complexity up to 13 so the planner accepts the complexity-8 story.
	cfg.Planning.MaxStoryComplexity = 13
	estimator := engine.NewEstimator(client, cfg, eventStore, projStore)

	est, err := estimator.Estimate(context.Background(), "Do things", dir, engine.EstimateOptions{})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}

	if est.Stories[0].Role != string(agent.RoleJunior) {
		t.Fatalf("expected junior for complexity 2, got %s", est.Stories[0].Role)
	}
	if est.Stories[1].Role != string(agent.RoleSenior) {
		t.Fatalf("expected senior for complexity 8, got %s", est.Stories[1].Role)
	}
}
