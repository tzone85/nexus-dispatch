//go:build e2e

package test

import (
	"context"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func replayScenarios() []Scenario {
	return []Scenario{
		scenarioHappyPath(),
		scenarioDiamondDeps(),
		scenarioFunctionCalling(),
	}
}

func scenarioHappyPath() Scenario {
	return Scenario{
		Name:        "happy_path_multi_story",
		Requirement: "Build a key-value store package with Get, Set, Delete, List operations. Thread-safe. Add HTTP API and tests.",
		Fixture:     FixtureConfig{},
		Replay: &ReplayConfig{
			Responses: []llm.CompletionResponse{
				happyPathPlannerResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
			},
		},
		Assertions: []Assertion{
			AssertStoriesCreated(3, 3),
			AssertComplexityRange(1, 13),
			AssertDependenciesValid(),
			AssertEventsEmitted(
				state.EventReqSubmitted,
				state.EventStoryCreated,
				state.EventReqPlanned,
			),
			AssertMinEvents(5),
		},
	}
}

func scenarioDiamondDeps() Scenario {
	return Scenario{
		Name:        "diamond_dependency_chain",
		Requirement: "Build a system with foundation types, storage layer, validation layer, and API that depends on both.",
		Fixture:     FixtureConfig{},
		ReplayOnly:  true,
		Replay: &ReplayConfig{
			Responses: []llm.CompletionResponse{
				diamondDepsPlannerResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
			},
		},
		Assertions: []Assertion{
			AssertStoriesCreated(4, 4),
			AssertEventsEmitted(state.EventStoryCreated, state.EventReqPlanned),
			AssertMinEvents(6),
		},
	}
}

func scenarioFunctionCalling() Scenario {
	return Scenario{
		Name:        "function_calling_round_trip",
		Requirement: "Build a key-value store with Get, Set, Delete, List. Thread-safe. HTTP API.",
		Fixture:     FixtureConfig{},
		Replay: &ReplayConfig{
			Responses: []llm.CompletionResponse{
				plannerToolCallResponse(),
				reviewerToolCallResponse(),
				reviewerToolCallResponse(),
				reviewerToolCallResponse(),
			},
		},
		Assertions: []Assertion{
			AssertStoriesCreated(3, 3),
			AssertComplexityRange(1, 13),
			AssertEventsEmitted(state.EventStoryCreated, state.EventReqPlanned),
		},
	}
}

func TestReplayScenarios(t *testing.T) {
	for _, scenario := range replayScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			RunScenarioWithAssertions(t, scenario, ModeReplay)
		})
	}
}

// TestReplayScenario_ReviewFailureRetry tests the review failure + retry path.
// The reviewer rejects the first review with feedback, then approves on the
// second attempt after simulated re-execution.
func TestReplayScenario_ReviewFailureRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	stores := CreateTestStores(t)
	repoPath := CreateFixtureRepo(t, FixtureConfig{})
	cfg := NewTestConfig(stores.StateDir)

	// Phase 1: Plan with a single story.
	planClient := llm.NewReplayClient(singleStoryPlannerResponse())
	planner := engine.NewPlanner(planClient, cfg, stores.Events, stores.Proj)
	reqID := "req-review-retry-001"

	_, err := planner.Plan(ctx, reqID, "Build a simple key-value store", repoPath)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	stories, err := stores.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	if len(stories) != 1 {
		t.Fatalf("expected 1 story, got %d", len(stories))
	}
	storyID := stories[0].ID

	// Phase 2: Simulate execution (mark story completed -> status "review").
	completeEvt := state.NewEvent(state.EventStoryCompleted, "agent-junior", storyID, nil)
	if err := stores.Events.Append(completeEvt); err != nil {
		t.Fatalf("append completion: %v", err)
	}
	if err := stores.Proj.Project(completeEvt); err != nil {
		t.Fatalf("project completion: %v", err)
	}

	// Phase 3: First review — REJECTS.
	rejectClient := llm.NewReplayClient(rejectReviewResponse("missing error handling in Get"))
	reviewer := engine.NewReviewer(
		rejectClient,
		cfg.Models.Senior.Provider,
		cfg.Models.Senior.Model,
		cfg.Models.Senior.MaxTokens,
		stores.Events, stores.Proj,
	)
	result, err := reviewer.Review(ctx, storyID, stories[0].Title, stories[0].AcceptanceCriteria, "mock diff v1")
	if err != nil {
		t.Fatalf("first review call failed: %v", err)
	}
	if result.Passed {
		t.Fatal("expected first review to fail, but it passed")
	}

	// Phase 4: Simulate re-execution (story goes back to review after fix).
	// STORY_REVIEW_FAILED projects status back to "draft". Simulate re-assignment
	// and re-completion.
	assignEvt := state.NewEvent(state.EventStoryAssigned, "agent-junior", storyID, map[string]any{
		"agent_id": "agent-junior",
		"wave":     1,
	})
	if err := stores.Events.Append(assignEvt); err != nil {
		t.Fatalf("append reassign: %v", err)
	}
	if err := stores.Proj.Project(assignEvt); err != nil {
		t.Fatalf("project reassign: %v", err)
	}

	completeEvt2 := state.NewEvent(state.EventStoryCompleted, "agent-junior", storyID, nil)
	if err := stores.Events.Append(completeEvt2); err != nil {
		t.Fatalf("append re-completion: %v", err)
	}
	if err := stores.Proj.Project(completeEvt2); err != nil {
		t.Fatalf("project re-completion: %v", err)
	}

	// Phase 5: Second review — APPROVES.
	approveClient := llm.NewReplayClient(approveReviewResponse())
	reviewer2 := engine.NewReviewer(
		approveClient,
		cfg.Models.Senior.Provider,
		cfg.Models.Senior.Model,
		cfg.Models.Senior.MaxTokens,
		stores.Events, stores.Proj,
	)
	result2, err := reviewer2.Review(ctx, storyID, stories[0].Title, stories[0].AcceptanceCriteria, "mock diff v2")
	if err != nil {
		t.Fatalf("second review call failed: %v", err)
	}
	if !result2.Passed {
		t.Fatal("expected second review to pass, but it failed")
	}

	// Assertions: verify events.
	events, err := stores.Events.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	var reviewFailedCount, reviewPassedCount int
	for _, e := range events {
		switch e.Type {
		case state.EventStoryReviewFailed:
			reviewFailedCount++
		case state.EventStoryReviewPassed:
			reviewPassedCount++
		}
	}

	if reviewFailedCount < 1 {
		t.Error("expected at least 1 STORY_REVIEW_FAILED event")
	}
	if reviewPassedCount < 1 {
		t.Error("expected at least 1 STORY_REVIEW_PASSED event")
	}
	if (reviewFailedCount + reviewPassedCount) < 2 {
		t.Errorf("expected at least 2 review events, got %d", reviewFailedCount+reviewPassedCount)
	}
}

// TestReplayScenario_QAFailureEscalation tests the QA failure and escalation
// event machinery. It manually emits the event sequence:
// STORY_CREATED -> STORY_ASSIGNED -> STORY_COMPLETED -> STORY_REVIEW_PASSED -> QA failure -> STORY_ESCALATED
// and verifies the correct events and projection state.
func TestReplayScenario_QAFailureEscalation(t *testing.T) {
	stores := CreateTestStores(t)
	reqID := "req-qa-esc-001"

	// Emit REQ_SUBMITTED.
	reqEvt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id":          reqID,
		"title":       "Build a simple utility",
		"description": "Build a simple utility",
		"repo_path":   "/tmp/fake-repo",
	})
	emitAndAssert(t, stores, reqEvt)

	// Emit STORY_CREATED (complexity 2 -> routes to Junior).
	storyID := reqID + "-s-001"
	storyEvt := state.NewEvent(state.EventStoryCreated, "tech-lead", storyID, map[string]any{
		"id":                  storyID,
		"req_id":              reqID,
		"title":               "Implement simple utility function",
		"description":         "Create a utility function",
		"acceptance_criteria": "Function compiles and passes tests",
		"complexity":          2,
		"depends_on":          []string{},
		"owned_files":         []string{"util.go"},
	})
	emitAndAssert(t, stores, storyEvt)

	// Emit STORY_ASSIGNED to junior agent.
	assignEvt := state.NewEvent(state.EventStoryAssigned, "dispatcher", storyID, map[string]any{
		"agent_id": "agent-junior-001",
		"wave":     1,
	})
	emitAndAssert(t, stores, assignEvt)

	// Emit STORY_COMPLETED (junior finishes, code has a build error).
	completeEvt := state.NewEvent(state.EventStoryCompleted, "agent-junior-001", storyID, nil)
	emitAndAssert(t, stores, completeEvt)

	// Emit STORY_REVIEW_PASSED (review passes but QA will fail).
	reviewEvt := state.NewEvent(state.EventStoryReviewPassed, "reviewer", storyID, map[string]any{
		"passed":        true,
		"comment_count": 0,
		"summary":       "Code looks fine structurally",
	})
	emitAndAssert(t, stores, reviewEvt)

	// Emit STORY_QA_STARTED.
	qaStartEvt := state.NewEvent(state.EventStoryQAStarted, "qa-agent", storyID, nil)
	emitAndAssert(t, stores, qaStartEvt)

	// Simulate QA failure: emit STORY_REVIEW_FAILED (build error detected by QA).
	qaFailEvt := state.NewEvent(state.EventStoryReviewFailed, "qa-agent", storyID, map[string]any{
		"passed":  false,
		"summary": "go build failed: undefined reference to missingFunc",
	})
	emitAndAssert(t, stores, qaFailEvt)

	// Emit STORY_ESCALATED (junior -> senior).
	escalateEvt := state.NewEvent(state.EventStoryEscalated, "agent-junior-001", storyID, map[string]any{
		"from_tier": 1,
		"to_tier":   2,
		"reason":    "QA failure: build error after review pass",
	})
	emitAndAssert(t, stores, escalateEvt)

	// Assertions: check events.
	events, err := stores.Events.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	expectedTypes := map[state.EventType]bool{
		state.EventReqSubmitted:      false,
		state.EventStoryCreated:      false,
		state.EventStoryAssigned:     false,
		state.EventStoryCompleted:    false,
		state.EventStoryReviewPassed: false,
		state.EventStoryQAStarted:    false,
		state.EventStoryReviewFailed: false,
		state.EventStoryEscalated:    false,
	}
	for _, e := range events {
		if _, ok := expectedTypes[e.Type]; ok {
			expectedTypes[e.Type] = true
		}
	}
	for et, found := range expectedTypes {
		if !found {
			t.Errorf("missing expected event: %s", et)
		}
	}

	// Verify escalation was recorded in projection.
	escalations, err := stores.Proj.ListEscalations()
	if err != nil {
		t.Fatalf("list escalations: %v", err)
	}
	if len(escalations) < 1 {
		t.Fatal("expected at least 1 escalation record")
	}
	esc := escalations[0]
	if esc.StoryID != storyID {
		t.Errorf("escalation story_id = %q, want %q", esc.StoryID, storyID)
	}
	if esc.FromTier != 1 || esc.ToTier != 2 {
		t.Errorf("escalation tiers = %d->%d, want 1->2", esc.FromTier, esc.ToTier)
	}

	// Verify story escalation_tier updated in projection.
	story, err := stores.Proj.GetStory(storyID)
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.EscalationTier != 2 {
		t.Errorf("story escalation_tier = %d, want 2", story.EscalationTier)
	}
}

// TestReplayScenario_PauseAndResume tests that REQ_PAUSED and REQ_RESUMED
// events correctly update the requirement status in projections.
func TestReplayScenario_PauseAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	stores := CreateTestStores(t)
	repoPath := CreateFixtureRepo(t, FixtureConfig{})
	cfg := NewTestConfig(stores.StateDir)

	// Phase 1: Plan with 2 stories (2 waves).
	planClient := llm.NewReplayClient(twoWavePlannerResponse())
	planner := engine.NewPlanner(planClient, cfg, stores.Events, stores.Proj)
	reqID := "req-pause-001"

	_, err := planner.Plan(ctx, reqID, "Build a system with types and storage", repoPath)
	if err != nil {
		t.Fatalf("Plan failed: %v", err)
	}

	// Verify initial status is "planned" (REQ_PLANNED was emitted by planner,
	// but the Planner only appends—doesn't project—the REQ_PLANNED event).
	// Project it manually.
	plannedEvt := state.NewEvent(state.EventReqPlanned, "tech-lead", "", map[string]any{"id": reqID})
	if err := stores.Proj.Project(plannedEvt); err != nil {
		t.Fatalf("project req planned: %v", err)
	}

	req, err := stores.Proj.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("get requirement: %v", err)
	}
	if req.Status != "planned" {
		t.Fatalf("requirement status after planning = %q, want %q", req.Status, "planned")
	}

	// Phase 2: Pause the requirement.
	pauseEvt := state.NewEvent(state.EventReqPaused, "user", "", map[string]any{"id": reqID})
	emitAndAssert(t, stores, pauseEvt)

	req, err = stores.Proj.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("get requirement after pause: %v", err)
	}
	if req.Status != "paused" {
		t.Errorf("requirement status after pause = %q, want %q", req.Status, "paused")
	}

	// Verify stories remain in "draft" while paused.
	stories, err := stores.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		t.Fatalf("list stories while paused: %v", err)
	}
	for _, s := range stories {
		if s.Status != "draft" {
			t.Errorf("story %s status while paused = %q, want %q", s.ID, s.Status, "draft")
		}
	}

	// Phase 3: Resume the requirement.
	resumeEvt := state.NewEvent(state.EventReqResumed, "user", "", map[string]any{"id": reqID})
	emitAndAssert(t, stores, resumeEvt)

	req, err = stores.Proj.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("get requirement after resume: %v", err)
	}
	if req.Status != "planned" {
		t.Errorf("requirement status after resume = %q, want %q", req.Status, "planned")
	}

	// Verify events were emitted.
	events, err := stores.Events.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	foundPaused, foundResumed := false, false
	for _, e := range events {
		switch e.Type {
		case state.EventReqPaused:
			foundPaused = true
		case state.EventReqResumed:
			foundResumed = true
		}
	}
	if !foundPaused {
		t.Error("expected REQ_PAUSED event not found")
	}
	if !foundResumed {
		t.Error("expected REQ_RESUMED event not found")
	}
}

// TestReplayScenario_FallbackClient tests the quota exhaustion fallback path.
// A mock primary client returns a quota error (429), and the FallbackClient
// transparently retries on the fallback (replay) client, which returns a valid
// planner response.
func TestReplayScenario_FallbackClient(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	stores := CreateTestStores(t)
	repoPath := CreateFixtureRepo(t, FixtureConfig{})
	cfg := NewTestConfig(stores.StateDir)

	// Primary client: always returns a quota error (simulates Google AI 429).
	primaryClient := llm.NewErrorClient(&llm.QuotaError{
		StatusCode: 429,
		Message:    "Resource has been exhausted (e.g. check quota).",
	})

	// Fallback client: returns a valid planner response.
	fallbackClient := llm.NewReplayClient(singleStoryPlannerResponse())

	// Build FallbackClient wrapping both.
	client := llm.NewFallbackClient(primaryClient, fallbackClient, 1*time.Second)

	// Run the planner through the fallback client.
	planner := engine.NewPlanner(client, cfg, stores.Events, stores.Proj)
	reqID := "req-fallback-001"

	_, err := planner.Plan(ctx, reqID, "Build a simple key-value store", repoPath)
	if err != nil {
		t.Fatalf("Plan through FallbackClient failed: %v", err)
	}

	// Verify: planning succeeded via fallback — stories were created.
	stories, err := stores.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		t.Fatalf("list stories: %v", err)
	}
	if len(stories) < 1 {
		t.Fatal("expected at least 1 story from fallback planning")
	}

	// Verify: correct events emitted.
	events, err := stores.Events.List(state.EventFilter{})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	foundSubmitted, foundCreated, foundPlanned := false, false, false
	for _, e := range events {
		switch e.Type {
		case state.EventReqSubmitted:
			foundSubmitted = true
		case state.EventStoryCreated:
			foundCreated = true
		case state.EventReqPlanned:
			foundPlanned = true
		}
	}
	if !foundSubmitted {
		t.Error("expected REQ_SUBMITTED event not found")
	}
	if !foundCreated {
		t.Error("expected STORY_CREATED event not found")
	}
	if !foundPlanned {
		t.Error("expected REQ_PLANNED event not found")
	}
}

// emitAndAssert appends an event and projects it, failing the test on error.
func emitAndAssert(t *testing.T, stores TestStores, evt state.Event) {
	t.Helper()
	if err := stores.Events.Append(evt); err != nil {
		t.Fatalf("append event %s: %v", evt.Type, err)
	}
	if err := stores.Proj.Project(evt); err != nil {
		t.Fatalf("project event %s: %v", evt.Type, err)
	}
}
