package test

import (
	"context"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Scenario describes a single E2E test case.
type Scenario struct {
	Name        string
	Requirement string
	Fixture     FixtureConfig
	Assertions  []Assertion
	Replay      *ReplayConfig
	LiveOnly    bool
	ReplayOnly  bool
}

// ReplayConfig holds canned LLM responses for deterministic testing.
type ReplayConfig struct {
	Responses []llm.CompletionResponse
}

// RunScenarioWithAssertions executes a scenario end-to-end in the given mode.
func RunScenarioWithAssertions(t *testing.T, scenario Scenario, mode Mode) {
	t.Helper()

	if mode == ModeReplay && scenario.LiveOnly {
		t.Skipf("scenario %q is live-only", scenario.Name)
	}
	if mode == ModeLive && scenario.ReplayOnly {
		t.Skipf("scenario %q is replay-only", scenario.Name)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 1. Setup fixture repo and stores.
	repoPath := CreateFixtureRepo(t, scenario.Fixture)
	stores := CreateTestStores(t)

	// 2. Build LLM client based on mode.
	var client llm.Client
	if mode == ModeReplay {
		if scenario.Replay == nil {
			t.Fatalf("scenario %q has no replay config", scenario.Name)
		}
		client = llm.NewReplayClient(scenario.Replay.Responses...)
	} else {
		RequireOllama(t)
		client = llm.NewOllamaClient("gemma3:4b")
	}

	// 3. Build config.
	cfg := NewTestConfig(stores.StateDir)

	// 4. Initialise TestState for assertions.
	ts := TestState{
		RepoPath: repoPath,
		StoreDir: stores.StateDir,
		Mode:     mode,
		Stores:   stores,
	}

	// --- Plan phase ---
	reqID := runPlanPhase(t, ctx, client, cfg, stores, repoPath, scenario.Requirement)
	ts.Refresh(t, reqID)
	runPhaseAssertions(t, scenario.Assertions, ts, "plan")

	// --- Simulate execution (replay only) ---
	if mode == ModeReplay {
		simulateExecution(t, stores, reqID)
	}
	ts.Refresh(t, reqID)

	// --- Review phase ---
	runReviewPhase(t, ctx, client, cfg, stores, reqID)
	ts.Refresh(t, reqID)
	runPhaseAssertions(t, scenario.Assertions, ts, "review")

	// --- Catch-all assertions ---
	runPhaseAssertions(t, scenario.Assertions, ts, "any")
}

// runPhaseAssertions runs every assertion whose Phase matches the given phase.
func runPhaseAssertions(t *testing.T, assertions []Assertion, ts TestState, phase string) {
	t.Helper()
	for _, a := range assertions {
		if a.Phase == phase {
			t.Run(a.Name, func(t *testing.T) { a.Check(t, ts) })
		}
	}
}

// runPlanPhase invokes the engine Planner and returns the generated request ID.
func runPlanPhase(
	t *testing.T,
	ctx context.Context,
	client llm.Client,
	cfg config.Config,
	stores TestStores,
	repoPath, requirement string,
) string {
	t.Helper()

	planner := engine.NewPlanner(client, cfg, stores.Events, stores.Proj)
	reqID := "req-test-001"

	_, err := planner.Plan(ctx, reqID, requirement, repoPath)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}
	return reqID
}

// simulateExecution marks every story in the requirement as completed by
// emitting STORY_COMPLETED events and projecting them.
func simulateExecution(t *testing.T, stores TestStores, reqID string) {
	t.Helper()

	stories, err := stores.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		t.Fatalf("list stories for simulation: %v", err)
	}

	for _, s := range stories {
		evt := state.NewEvent(state.EventStoryCompleted, "", s.ID, nil)
		if err := stores.Events.Append(evt); err != nil {
			t.Fatalf("append completion event for %s: %v", s.ID, err)
		}
		if err := stores.Proj.Project(evt); err != nil {
			t.Fatalf("project completion event for %s: %v", s.ID, err)
		}
	}
}

// runReviewPhase runs a code review for every story in the requirement.
func runReviewPhase(
	t *testing.T,
	ctx context.Context,
	client llm.Client,
	cfg config.Config,
	stores TestStores,
	reqID string,
) {
	t.Helper()

	stories, err := stores.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		t.Fatalf("list stories for review: %v", err)
	}

	reviewer := engine.NewReviewer(
		client,
		cfg.Models.Senior.Provider,
		cfg.Models.Senior.Model,
		cfg.Models.Senior.MaxTokens,
		stores.Events,
		stores.Proj,
	)

	for _, s := range stories {
		_, err := reviewer.Review(ctx, s.ID, s.Title, s.AcceptanceCriteria, "mock diff for testing")
		if err != nil {
			t.Logf("review for story %s: %v (may be expected in some scenarios)", s.ID, err)
		}
	}
}
