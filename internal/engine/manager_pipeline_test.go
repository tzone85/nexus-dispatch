package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// setupManagerPipelineTest creates the test infrastructure for manager
// pipeline tests: stores, a story at tier 2, and the escalation history.
func setupManagerPipelineTest(t *testing.T, storyID, reqID string) (state.EventStore, state.ProjectionStore, func()) {
	t.Helper()
	es, ps, cleanup := newTestStores(t)

	// Create requirement.
	ps.Project(state.NewEvent(state.EventReqSubmitted, "user", "", map[string]any{
		"id": reqID, "title": "Test req", "description": "desc",
	}))
	ps.Project(state.NewEvent(state.EventReqPlanned, "tech-lead", "", map[string]any{
		"id": reqID,
	}))

	// Create story.
	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", storyID, map[string]any{
		"id": storyID, "req_id": reqID, "title": "Test story",
		"description": "Do something", "complexity": 5,
		"acceptance_criteria": "It works",
	}))

	// Escalate to tier 2 (0->1, then 1->2).
	for _, esc := range []struct{ from, to int }{{0, 1}, {1, 2}} {
		evt := state.NewEvent(state.EventStoryEscalated, "reviewer", storyID, map[string]any{
			"from_tier": esc.from,
			"to_tier":   esc.to,
			"reason":    fmt.Sprintf("review failed at tier %d", esc.from),
		})
		es.Append(evt)
		ps.Project(evt)
	}

	return es, ps, cleanup
}

// buildManagerMonitorWithAutoResume creates a Monitor with a Manager and
// auto-resume enabled (dispatcher + executor).
func buildManagerMonitorWithAutoResume(
	t *testing.T,
	es state.EventStore,
	ps state.ProjectionStore,
	llmClient llm.Client,
) *engine.Monitor {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Monitor.PollIntervalMs = 10

	reg, err := newTestRegistry()
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	wd := engine.NewWatchdog(engine.WatchdogConfig{StuckThresholdS: 120}, es)

	mon := engine.NewMonitor(reg, wd, nil, nil, nil, cfg, es, ps)
	mgr := engine.NewManager(llmClient, "ollama", "test-model", 4000, es, ps)
	mon.SetManager(mgr)

	dispatcher := engine.NewDispatcher(cfg, es, ps)
	executor := engine.NewExecutor(reg, cfg, es, ps)
	mon.SetAutoResume(dispatcher, executor)

	return mon
}

// TestManagerPipeline_RetryAction verifies that after a manager retry action,
// the story's escalation events reflect the de-escalation and the story is
// reset to draft for re-dispatch.
func TestManagerPipeline_RetryAction(t *testing.T) {
	storyID := "s-mgr-retry"
	reqID := "r-mgr-retry"
	es, ps, cleanup := setupManagerPipelineTest(t, storyID, reqID)
	defer cleanup()

	// Simulate what executeRetryAction emits.
	retryEvt := state.NewEvent(state.EventStoryEscalated, "manager", storyID, map[string]any{
		"from_tier": 2,
		"to_tier":   0,
		"reason":    "manager retry: environment issue",
	})
	es.Append(retryEvt)
	ps.Project(retryEvt)

	resetEvt := state.NewEvent(state.EventStoryReviewFailed, "manager", storyID, map[string]any{
		"reason": "manager retry with fixes",
	})
	es.Append(resetEvt)
	ps.Project(resetEvt)

	// Verify story is reset to draft.
	story, err := ps.GetStory(storyID)
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Status != "draft" {
		t.Fatalf("expected story status 'draft' after retry, got %q", story.Status)
	}

	// Verify escalation events: 0->1, 1->2, 2->0 (retry).
	events, err := es.List(state.EventFilter{Type: state.EventStoryEscalated, StoryID: storyID})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 escalation events, got %d", len(events))
	}

	var payload map[string]any
	json.Unmarshal(events[2].Payload, &payload)
	if toTier, ok := payload["to_tier"].(float64); !ok || int(toTier) != 0 {
		t.Fatalf("expected to_tier 0 in retry event, got %v", payload["to_tier"])
	}
}

// TestManagerPipeline_RewriteAction verifies that the rewrite action updates
// the story's title, description, etc. via a STORY_REWRITTEN event.
func TestManagerPipeline_RewriteAction(t *testing.T) {
	storyID := "s-mgr-rewrite"
	reqID := "r-mgr-rewrite"
	es, ps, cleanup := setupManagerPipelineTest(t, storyID, reqID)
	defer cleanup()

	// Simulate what executeRewriteAction emits.
	changes := map[string]any{
		"title":               "Better title",
		"description":         "Better description",
		"acceptance_criteria": "Updated AC",
		"complexity":          3,
	}
	evt := state.NewEvent(state.EventStoryRewritten, "manager", storyID, map[string]any{
		"changes": changes,
		"reason":  "story was too vague",
	})
	es.Append(evt)
	ps.Project(evt)

	story, err := ps.GetStory(storyID)
	if err != nil {
		t.Fatalf("get story: %v", err)
	}
	if story.Title != "Better title" {
		t.Fatalf("expected rewritten title, got %q", story.Title)
	}
	if story.Description != "Better description" {
		t.Fatalf("expected rewritten description, got %q", story.Description)
	}
}

// TestManagerPipeline_SplitAction verifies that the split action creates child
// stories and marks the parent as "split".
func TestManagerPipeline_SplitAction(t *testing.T) {
	storyID := "s-mgr-split"
	reqID := "r-mgr-split"
	es, ps, cleanup := setupManagerPipelineTest(t, storyID, reqID)
	defer cleanup()

	// Simulate what executeSplitAction emits.
	childA := state.NewEvent(state.EventStoryCreated, "manager", storyID+"-a", map[string]any{
		"id":                  storyID + "-a",
		"req_id":              reqID,
		"title":               "Part A",
		"description":         "First half",
		"acceptance_criteria": "A works",
		"complexity":          3,
		"owned_files":         []string{"a.go"},
		"split_depth":         1,
	})
	es.Append(childA)
	ps.Project(childA)

	childB := state.NewEvent(state.EventStoryCreated, "manager", storyID+"-b", map[string]any{
		"id":                  storyID + "-b",
		"req_id":              reqID,
		"title":               "Part B",
		"description":         "Second half",
		"acceptance_criteria": "B works",
		"complexity":          3,
		"owned_files":         []string{"b.go"},
		"split_depth":         1,
	})
	es.Append(childB)
	ps.Project(childB)

	splitEvt := state.NewEvent(state.EventStorySplit, "manager", storyID, map[string]any{
		"child_story_ids": []string{storyID + "-a", storyID + "-b"},
		"reason":          "too complex",
	})
	es.Append(splitEvt)
	ps.Project(splitEvt)

	parent, err := ps.GetStory(storyID)
	if err != nil {
		t.Fatalf("get parent story: %v", err)
	}
	if parent.Status != "split" {
		t.Fatalf("expected parent status 'split', got %q", parent.Status)
	}

	childStory, err := ps.GetStory(storyID + "-a")
	if err != nil {
		t.Fatalf("get child story a: %v", err)
	}
	if childStory.Title != "Part A" {
		t.Fatalf("expected child title 'Part A', got %q", childStory.Title)
	}
	if childStory.SplitDepth != 1 {
		t.Fatalf("expected child split_depth 1, got %d", childStory.SplitDepth)
	}
}

// TestManagerPipeline_EscalateToTechLead verifies that escalation from
// tier 2 to tier 3 emits the correct STORY_ESCALATED event.
func TestManagerPipeline_EscalateToTechLead(t *testing.T) {
	storyID := "s-mgr-esc"
	reqID := "r-mgr-esc"
	es, ps, cleanup := setupManagerPipelineTest(t, storyID, reqID)
	defer cleanup()

	// Simulate what escalateToTier(storyID, 3, ...) emits.
	evt := state.NewEvent(state.EventStoryEscalated, "monitor", storyID, map[string]any{
		"from_tier": 2,
		"to_tier":   3,
		"reason":    "manager escalated: structural problem",
	})
	es.Append(evt)
	ps.Project(evt)

	events, err := es.List(state.EventFilter{Type: state.EventStoryEscalated, StoryID: storyID})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	// 3 total: 0->1, 1->2, 2->3.
	if len(events) != 3 {
		t.Fatalf("expected 3 escalation events, got %d", len(events))
	}

	var payload map[string]any
	json.Unmarshal(events[2].Payload, &payload)
	if toTier, ok := payload["to_tier"].(float64); !ok || int(toTier) != 3 {
		t.Fatalf("expected to_tier 3, got %v", payload["to_tier"])
	}
}

// TestManagerPipeline_FatalAPIError_PausesRequirement verifies that a fatal
// API error during manager diagnosis pauses the requirement via the
// monitor's auto-resume -> tier interception path.
func TestManagerPipeline_FatalAPIError_PausesRequirement(t *testing.T) {
	storyID := "s-mgr-fatal"
	reqID := "r-mgr-fatal"
	es, ps, cleanup := setupManagerPipelineTest(t, storyID, reqID)
	defer cleanup()

	fatalClient := llm.NewErrorClient(&llm.APIError{
		StatusCode: 401,
		Message:    "invalid api key",
		Retryable:  false,
	})

	mon := buildManagerMonitorWithAutoResume(t, es, ps, fatalClient)

	dag := graph.New()
	dag.AddNode(storyID)

	rc := &engine.RunContext{
		ReqID: reqID,
		PlannedStories: []engine.PlannedStory{
			{ID: storyID, Title: "Test story", Complexity: 5},
		},
		DAG:        dag,
		WaveNumber: 1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = mon.RunWithContext(ctx, []engine.ActiveAgent{}, "/tmp/repo", rc)

	req, err := ps.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("get requirement: %v", err)
	}
	if req.Status != "paused" {
		t.Fatalf("expected requirement status 'paused', got %q", req.Status)
	}
}

// TestManagerPipeline_Tier0_NotIntercepted verifies that tier-0 stories
// are NOT intercepted by the manager and instead flow through to the
// dispatcher normally.
func TestManagerPipeline_Tier0_NotIntercepted(t *testing.T) {
	storyID := "s-tier0"
	reqID := "r-tier0"
	es, ps, cleanup := newTestStores(t)
	defer cleanup()

	// Create requirement.
	ps.Project(state.NewEvent(state.EventReqSubmitted, "user", "", map[string]any{
		"id": reqID, "title": "Test req", "description": "desc",
	}))
	ps.Project(state.NewEvent(state.EventReqPlanned, "tech-lead", "", map[string]any{
		"id": reqID,
	}))

	// Create story at tier 0 (no escalation events).
	ps.Project(state.NewEvent(state.EventStoryCreated, "tech-lead", storyID, map[string]any{
		"id": storyID, "req_id": reqID, "title": "Simple task",
		"description": "Easy thing", "complexity": 2,
	}))

	// Manager that should NOT be called.
	replayClient := llm.NewReplayClient() // No responses -- will error if called.
	mon := buildManagerMonitorWithAutoResume(t, es, ps, replayClient)

	dag := graph.New()
	dag.AddNode(storyID)

	rc := &engine.RunContext{
		ReqID: reqID,
		PlannedStories: []engine.PlannedStory{
			{ID: storyID, Title: "Simple task", Complexity: 2},
		},
		DAG:        dag,
		WaveNumber: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_ = mon.RunWithContext(ctx, []engine.ActiveAgent{}, "/tmp/repo", rc)

	// Verify the manager was NOT called (replay client has no responses,
	// calling it would error).
	if replayClient.CallCount() != 0 {
		t.Fatalf("expected manager NOT to be called for tier-0 story, got %d calls", replayClient.CallCount())
	}

	// The story should have been dispatched normally (STORY_ASSIGNED event).
	assignedEvents, err := es.List(state.EventFilter{Type: state.EventStoryAssigned, StoryID: storyID})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(assignedEvents) != 1 {
		t.Fatalf("expected 1 STORY_ASSIGNED event for tier-0 story, got %d", len(assignedEvents))
	}
}
