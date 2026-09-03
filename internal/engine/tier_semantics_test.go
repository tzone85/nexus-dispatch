package engine

import (
	"context"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// escalateTo appends a STORY_ESCALATED event and projects it when ps is set.
func escalateTo(t *testing.T, es state.EventStore, ps state.ProjectionStore, storyID string, from, to int) {
	t.Helper()
	evt := state.NewEvent(state.EventStoryEscalated, "test", storyID, map[string]any{
		"from_tier": from, "to_tier": to, "reason": "test",
	})
	if err := es.Append(evt); err != nil {
		t.Fatal(err)
	}
	if ps != nil {
		if err := ps.Project(evt); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCurrentTier_LatestWins is the core of the tier-semantics fix: a manager
// retry that sends a story back to tier 0 must actually lower the tier. The
// old implementation took the MAX to_tier ever seen, so a 0→1→2→0 history
// still reported tier 2 and the story was intercepted by the manager forever.
func TestCurrentTier_LatestWins(t *testing.T) {
	tests := []struct {
		name  string
		tiers [][2]int
		want  int
	}{
		{"no escalation", nil, 0},
		{"single upward", [][2]int{{0, 1}}, 1},
		{"two upward", [][2]int{{0, 1}, {1, 2}}, 2},
		{"manager retry lowers tier", [][2]int{{0, 1}, {1, 2}, {2, 0}}, 0},
		{"lowered then re-escalated", [][2]int{{0, 1}, {1, 2}, {2, 0}, {0, 1}}, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := testEscalationStore(t)
			for _, pair := range tc.tiers {
				escalateTo(t, fs, nil, "s-1", pair[0], pair[1])
			}
			esc := NewEscalationMachine(fs, defaultRoutingConfig())
			got, err := esc.CurrentTier("s-1")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("CurrentTier = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestCurrentTier_IgnoresEventsWithoutToTier(t *testing.T) {
	fs := testEscalationStore(t)
	escalateTo(t, fs, nil, "s-1", 0, 1)
	if err := fs.Append(state.NewEvent(state.EventStoryEscalated, "test", "s-1", map[string]any{"reason": "malformed"})); err != nil {
		t.Fatal(err)
	}
	esc := NewEscalationMachine(fs, defaultRoutingConfig())
	got, err := esc.CurrentTier("s-1")
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("CurrentTier = %d, want 1 (malformed trailing event ignored)", got)
	}
}

// TestRouteStory_AfterManagerRetry_RoutesByComplexity aligns the dispatcher
// with the escalation machine: a story lowered back to tier 0 by the manager
// is routed by complexity again, not force-routed to senior.
func TestRouteStory_AfterManagerRetry_RoutesByComplexity(t *testing.T) {
	fs := testEscalationStore(t)
	escalateTo(t, fs, nil, "s-1", 0, 1)
	escalateTo(t, fs, nil, "s-1", 1, 2)
	escalateTo(t, fs, nil, "s-1", 2, 0)

	d := &Dispatcher{
		eventStore: fs,
		config: config.Config{
			Routing: config.RoutingConfig{JuniorMaxComplexity: 3, IntermediateMaxComplexity: 5},
		},
	}
	if role := d.routeStory(PlannedStory{ID: "s-1", Complexity: 2}); role != agent.RoleJunior {
		t.Fatalf("tier 0 after retry should route by complexity (junior), got %s", role)
	}
}

// TestExecuteRetryAction_RetryLoop drives the manager retry end-to-end at the
// unit level: after executeRetryAction the story must be at tier 0 with a
// clean retry budget, and dispatchNextWave must hand it to the dispatcher
// (STORY_ASSIGNED) rather than intercepting it for the manager again.
func TestExecuteRetryAction_RetryLoop(t *testing.T) {
	es, ps := newAttemptStores(t)
	const storyID, reqID = "s-retry", "r-retry"

	for _, evt := range []state.Event{
		state.NewEvent(state.EventReqSubmitted, "user", "", map[string]any{"id": reqID, "title": "t", "description": "d"}),
		state.NewEvent(state.EventStoryCreated, "tech-lead", storyID, map[string]any{
			"id": storyID, "req_id": reqID, "title": "Retry me", "description": "d", "complexity": 2,
		}),
	} {
		if err := es.Append(evt); err != nil {
			t.Fatal(err)
		}
		if err := ps.Project(evt); err != nil {
			t.Fatal(err)
		}
	}
	// Two review failures per tier got the story to tier 2.
	for i := 0; i < 2; i++ {
		fail := state.NewEvent(state.EventStoryReviewFailed, "reviewer", storyID, map[string]any{"reason": "nope"})
		es.Append(fail)
		ps.Project(fail)
	}
	escalateTo(t, es, ps, storyID, 0, 1)
	for i := 0; i < 2; i++ {
		fail := state.NewEvent(state.EventStoryReviewFailed, "reviewer", storyID, map[string]any{"reason": "nope"})
		es.Append(fail)
		ps.Project(fail)
	}
	escalateTo(t, es, ps, storyID, 1, 2)
	time.Sleep(2 * time.Millisecond) // the retry events must sort after the history

	cfg := config.DefaultConfig()
	reg, err := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, nil, nil, cfg, es, ps)
	// A manager whose LLM has no responses: if the story is intercepted again
	// Diagnose fails and the monitor resets the story, which the assertions
	// below detect via a fresh STORY_REVIEW_FAILED.
	m.SetManager(NewManager(llm.NewReplayClient(), "ollama", "m", 100, es, ps))
	m.SetAutoResume(NewDispatcher(cfg, es, ps), NewExecutor(reg, cfg, es, ps, nil))

	m.executeRetryAction(storyID, ManagerAction{
		Diagnosis:   "environment issue",
		Action:      "retry",
		RetryConfig: &RetryConfig{ResetTier: 0},
	}, t.TempDir())

	tier, err := m.escalation.CurrentTier(storyID)
	if err != nil {
		t.Fatal(err)
	}
	if tier != 0 {
		t.Fatalf("after retry CurrentTier = %d, want 0", tier)
	}
	retries, err := m.escalation.RetryCountAtCurrentTier(storyID)
	if err != nil {
		t.Fatal(err)
	}
	if retries != 0 {
		t.Fatalf("retry must not consume the tier-0 retry budget, RetryCountAtCurrentTier = %d", retries)
	}
	story, err := ps.GetStory(storyID)
	if err != nil {
		t.Fatal(err)
	}
	if story.Status != "draft" {
		t.Fatalf("story status = %q, want draft", story.Status)
	}
	if story.EscalationTier != 0 {
		t.Fatalf("projected escalation tier = %d, want 0", story.EscalationTier)
	}

	failsBefore, _ := es.Count(state.EventFilter{Type: state.EventStoryReviewFailed, StoryID: storyID})

	dag := graph.New()
	dag.AddNode(storyID)
	rc := &RunContext{
		ReqID:          reqID,
		PlannedStories: []PlannedStory{{ID: storyID, Title: "Retry me", Complexity: 2, OwnedFiles: []string{"a.go"}}},
		DAG:            dag,
	}
	_ = m.dispatchNextWave(context.Background(), rc, t.TempDir())

	assigned, _ := es.List(state.EventFilter{Type: state.EventStoryAssigned, StoryID: storyID})
	if len(assigned) != 1 {
		t.Fatalf("expected the retried story to be dispatched (1 STORY_ASSIGNED), got %d", len(assigned))
	}
	if got := state.DecodePayload(assigned[0].Payload)["wave"]; got != float64(1) {
		t.Fatalf("expected wave 1, got %v", got)
	}
	failsAfter, _ := es.Count(state.EventFilter{Type: state.EventStoryReviewFailed, StoryID: storyID})
	if failsAfter != failsBefore {
		t.Fatalf("story was intercepted by the manager again: STORY_REVIEW_FAILED went %d → %d", failsBefore, failsAfter)
	}
}

// TestResetStoryToDraftFor_StampsAttempt checks the post-execution pipeline's
// reset path stamps the attempt on the events it emits (STORY_REVIEW_FAILED,
// and STORY_ESCALATED when the tier flips).
func TestResetStoryToDraftFor_StampsAttempt(t *testing.T) {
	es, ps := newAttemptStores(t)
	const storyID = "s-att"
	created := state.NewEvent(state.EventStoryCreated, "tl", storyID, map[string]any{
		"id": storyID, "req_id": "r", "title": "t", "description": "d", "complexity": 2,
	})
	es.Append(created)
	ps.Project(created)

	cfg := config.DefaultConfig()
	cfg.Routing.MaxRetriesBeforeEscalation = 1
	reg, _ := runtime.NewRegistry(map[string]config.RuntimeConfig{})
	m := NewMonitor(reg, NewWatchdog(WatchdogConfig{StuckThresholdS: 120}, es), nil, nil, nil, cfg, es, ps)

	// First failure: plain reset within tier 0.
	m.resetStoryToDraftFor(storyID, "s-att-a1", "reviewer", "review rejected")
	fails, _ := es.List(state.EventFilter{Type: state.EventStoryReviewFailed, StoryID: storyID})
	if len(fails) != 1 || fails[0].AttemptID != "s-att-a1" {
		t.Fatalf("expected one STORY_REVIEW_FAILED stamped s-att-a1, got %+v", fails)
	}

	// Second failure: retry budget (1) exhausted → escalate to tier 1.
	m.resetStoryToDraftFor(storyID, "s-att-a2", "reviewer", "review rejected again")
	escs, _ := es.List(state.EventFilter{Type: state.EventStoryEscalated, StoryID: storyID})
	if len(escs) != 1 || escs[0].AttemptID != "s-att-a2" {
		t.Fatalf("expected one STORY_ESCALATED stamped s-att-a2, got %+v", escs)
	}
	fails, _ = es.List(state.EventFilter{Type: state.EventStoryReviewFailed, StoryID: storyID, AttemptID: "s-att-a2"})
	if len(fails) != 1 {
		t.Fatalf("expected the escalation's reset STORY_REVIEW_FAILED stamped s-att-a2, got %d", len(fails))
	}

	// Legacy entry point keeps working with no attempt.
	m.resetStoryToDraft(storyID, "manager", "no attempt in scope")
	all, _ := es.List(state.EventFilter{Type: state.EventStoryReviewFailed, StoryID: storyID})
	if all[len(all)-1].AttemptID != "" {
		t.Fatalf("legacy reset must not invent an attempt id, got %q", all[len(all)-1].AttemptID)
	}
}
