package engine

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newControllerTestStores creates temp event and projection stores for
// controller testing. Returns concrete *SQLiteStore so controller tests
// can call GetStory (not on the ProjectionStore interface).
func newControllerTestStores(t *testing.T) (state.EventStore, *state.SQLiteStore) {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ps, err := state.NewSQLiteStore(filepath.Join(dir, "proj.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		es.Close()
		ps.Close()
	})
	return es, ps
}

// seedInProgressStory creates a story in "in_progress" status and returns its ID.
func seedInProgressStory(t *testing.T, es state.EventStore, ps *state.SQLiteStore, reqID, storyID string) {
	t.Helper()

	// Create story.
	evt := state.NewEvent(state.EventStoryCreated, "system", storyID, map[string]any{
		"id":          storyID,
		"req_id":      reqID,
		"title":       "Test Story " + storyID,
		"description": "A test story",
		"complexity":  3,
	})
	es.Append(evt)
	ps.Project(evt)

	// Move to in_progress.
	startEvt := state.NewEvent(state.EventStoryStarted, "agent-1", storyID, nil)
	es.Append(startEvt)
	ps.Project(startEvt)
}

// seedReq creates a requirement and returns its ID.
func seedReq(t *testing.T, es state.EventStore, ps *state.SQLiteStore) string {
	t.Helper()
	id := "req-ctrl-001"
	evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id":          id,
		"title":       "Controller Test Req",
		"description": "Testing controller",
		"repo_path":   "/tmp/test",
	})
	es.Append(evt)
	ps.Project(evt)
	return id
}

func TestController_DecideAction_AutoCancel(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{
		Enabled:    true,
		AutoCancel: true,
	}
	ctrl := NewController(cfg, nil, es, ps)

	story := state.Story{ID: "s-001"}
	action := ctrl.decideAction(story)
	if action == nil {
		t.Fatal("expected action, got nil")
	}
	if action.Kind != ActionCancel {
		t.Errorf("expected ActionCancel, got %q", action.Kind)
	}
}

func TestController_DecideAction_AutoRestart(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{
		Enabled:     true,
		AutoRestart: true,
	}
	ctrl := NewController(cfg, nil, es, ps)

	story := state.Story{ID: "s-001"}
	action := ctrl.decideAction(story)
	if action == nil {
		t.Fatal("expected action, got nil")
	}
	if action.Kind != ActionRestart {
		t.Errorf("expected ActionRestart, got %q", action.Kind)
	}
}

func TestController_DecideAction_AutoReprioritize(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{
		Enabled:          true,
		AutoReprioritize: true,
	}
	ctrl := NewController(cfg, nil, es, ps)

	story := state.Story{ID: "s-001"}
	action := ctrl.decideAction(story)
	if action == nil {
		t.Fatal("expected action, got nil")
	}
	if action.Kind != ActionReprioritize {
		t.Errorf("expected ActionReprioritize, got %q", action.Kind)
	}
}

func TestController_DecideAction_ReprioritizeTakesPriority(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{
		Enabled:          true,
		AutoCancel:       true,
		AutoRestart:      true,
		AutoReprioritize: true,
	}
	ctrl := NewController(cfg, nil, es, ps)

	story := state.Story{ID: "s-001"}
	action := ctrl.decideAction(story)
	if action == nil {
		t.Fatal("expected action, got nil")
	}
	// Reprioritize should win over restart and cancel.
	if action.Kind != ActionReprioritize {
		t.Errorf("expected ActionReprioritize, got %q", action.Kind)
	}
}

func TestController_DecideAction_NothingEnabled(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{Enabled: true}
	ctrl := NewController(cfg, nil, es, ps)

	story := state.Story{ID: "s-001"}
	action := ctrl.decideAction(story)
	if action != nil {
		t.Errorf("expected nil action when no auto-actions enabled, got %v", action)
	}
}

func TestController_LastProgressTime_UsesProgressEvents(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)

	storyID := "s-prog-001"

	// Add a STORY_STARTED event.
	startEvt := state.NewEvent(state.EventStoryStarted, "agent-1", storyID, nil)
	es.Append(startEvt)

	// Add a more recent STORY_PROGRESS event.
	time.Sleep(10 * time.Millisecond)
	progEvt := state.NewEvent(state.EventStoryProgress, "agent-1", storyID, map[string]any{
		"iteration": 3,
	})
	es.Append(progEvt)

	lastProg := ctrl.lastProgressTime(storyID)
	if lastProg.IsZero() {
		t.Fatal("expected non-zero lastProgressTime")
	}
	// Should use the progress event, not the started event.
	if lastProg.Before(startEvt.Timestamp) {
		t.Error("expected lastProgressTime to be after STORY_STARTED")
	}
}

func TestController_LastProgressTime_FallsBackToStarted(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)

	storyID := "s-start-001"
	startEvt := state.NewEvent(state.EventStoryStarted, "agent-1", storyID, nil)
	es.Append(startEvt)

	lastProg := ctrl.lastProgressTime(storyID)
	if lastProg.IsZero() {
		t.Fatal("expected non-zero lastProgressTime from STORY_STARTED fallback")
	}
}

func TestController_LastProgressTime_NoEvents(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)

	lastProg := ctrl.lastProgressTime("nonexistent")
	if !lastProg.IsZero() {
		t.Error("expected zero time for story with no events")
	}
}

func TestController_CancelStory_InvokesCancelFunc(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)

	cancelled := false
	ctrl.RegisterCancel("s-001", func() { cancelled = true })

	ctrl.cancelStory("s-001")
	if !cancelled {
		t.Error("expected cancel function to be called")
	}

	// Verify cancel func was removed.
	ctrl.mu.Lock()
	_, exists := ctrl.cancelFuncs["s-001"]
	ctrl.mu.Unlock()
	if exists {
		t.Error("expected cancel func to be removed after cancellation")
	}

	// Verify AGENT_TERMINATED event was emitted.
	events, _ := es.List(state.EventFilter{Type: state.EventAgentTerminated})
	if len(events) == 0 {
		t.Error("expected AGENT_TERMINATED event to be emitted")
	}
}

func TestController_CancelStory_NoopForUnknown(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)

	// Should not panic for unknown story.
	ctrl.cancelStory("nonexistent")

	// Still emits a termination event.
	events, _ := es.List(state.EventFilter{Type: state.EventAgentTerminated})
	if len(events) == 0 {
		t.Error("expected AGENT_TERMINATED event even for unknown cancel func")
	}
}

func TestController_ResetStoryToDraft(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)

	reqID := seedReq(t, es, ps)
	seedInProgressStory(t, es, ps, reqID, "s-reset-001")

	ctrl.resetStoryToDraft("s-reset-001", "test reset")

	story, err := ps.GetStory("s-reset-001")
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	if story.Status != "draft" {
		t.Errorf("expected status=draft after reset, got %q", story.Status)
	}
}

func TestController_ReprioritizeStory(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)

	reqID := seedReq(t, es, ps)
	seedInProgressStory(t, es, ps, reqID, "s-repri-001")

	ctrl.reprioritizeStory("s-repri-001", "test reprioritize")

	story, err := ps.GetStory("s-repri-001")
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	// Should be escalated to tier 1 and reset to draft.
	if story.EscalationTier != 1 {
		t.Errorf("expected escalation_tier=1, got %d", story.EscalationTier)
	}
	if story.Status != "draft" {
		t.Errorf("expected status=draft, got %q", story.Status)
	}

	// Verify escalation event was emitted.
	events, _ := es.List(state.EventFilter{Type: state.EventStoryEscalated, StoryID: "s-repri-001"})
	if len(events) == 0 {
		t.Error("expected STORY_ESCALATED event")
	}
}

func TestController_Tick_DetectsStuckStory(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{
		Enabled:           true,
		MaxStuckDurationS: 0, // 0 seconds = everything is stuck
		AutoCancel:        true,
		MaxActionsPerTick: 5,
		CooldownS:         0,
	}
	ctrl := NewController(cfg, nil, es, ps)

	reqID := seedReq(t, es, ps)
	seedInProgressStory(t, es, ps, reqID, "s-stuck-001")

	// Tick should detect the stuck story and emit events.
	ctrl.tick(context.Background())

	// Check for CONTROLLER_STUCK_DETECTED event.
	stuckEvents, _ := es.List(state.EventFilter{Type: state.EventControllerStuckDetected})
	if len(stuckEvents) == 0 {
		t.Error("expected CONTROLLER_STUCK_DETECTED event")
	}

	// Check for CONTROLLER_ACTION event.
	actionEvents, _ := es.List(state.EventFilter{Type: state.EventControllerAction})
	if len(actionEvents) == 0 {
		t.Error("expected CONTROLLER_ACTION event")
	}

	// Check for CONTROLLER_ANALYSIS event.
	analysisEvents, _ := es.List(state.EventFilter{Type: state.EventControllerAnalysis})
	if len(analysisEvents) == 0 {
		t.Error("expected CONTROLLER_ANALYSIS event")
	}
}

func TestController_Tick_RespectsMaxActionsPerTick(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{
		Enabled:           true,
		MaxStuckDurationS: 0,
		AutoCancel:        true,
		MaxActionsPerTick: 1, // only 1 action per tick
		CooldownS:         0,
	}
	ctrl := NewController(cfg, nil, es, ps)

	reqID := seedReq(t, es, ps)
	seedInProgressStory(t, es, ps, reqID, "s-max-001")
	seedInProgressStory(t, es, ps, reqID, "s-max-002")

	ctrl.tick(context.Background())

	// Should only have taken 1 action despite 2 stuck stories.
	actionEvents, _ := es.List(state.EventFilter{Type: state.EventControllerAction})
	if len(actionEvents) != 1 {
		t.Errorf("expected 1 action event (max_actions_per_tick=1), got %d", len(actionEvents))
	}
}

func TestController_Tick_RespectsCooldown(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{
		Enabled:           true,
		MaxStuckDurationS: 0,
		AutoCancel:        true,
		MaxActionsPerTick: 5,
		CooldownS:         3600, // 1 hour cooldown
	}
	ctrl := NewController(cfg, nil, es, ps)

	reqID := seedReq(t, es, ps)
	seedInProgressStory(t, es, ps, reqID, "s-cool-001")

	// First tick should take action.
	ctrl.tick(context.Background())
	actionEvents1, _ := es.List(state.EventFilter{Type: state.EventControllerAction})
	if len(actionEvents1) == 0 {
		t.Fatal("expected action on first tick")
	}

	// Re-seed story as in_progress (since cancel moved it).
	seedInProgressStory(t, es, ps, reqID, "s-cool-002")

	// Second tick should be suppressed by cooldown.
	ctrl.tick(context.Background())
	actionEvents2, _ := es.List(state.EventFilter{Type: state.EventControllerAction})
	if len(actionEvents2) != len(actionEvents1) {
		t.Errorf("expected cooldown to prevent second action; actions before=%d, after=%d",
			len(actionEvents1), len(actionEvents2))
	}
}

func TestController_Tick_SkipsHealthyStories(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{
		Enabled:           true,
		MaxStuckDurationS: 3600, // 1 hour threshold — nothing should be stuck
		AutoCancel:        true,
		MaxActionsPerTick: 5,
		CooldownS:         0,
	}
	ctrl := NewController(cfg, nil, es, ps)

	reqID := seedReq(t, es, ps)
	seedInProgressStory(t, es, ps, reqID, "s-healthy-001")

	ctrl.tick(context.Background())

	// No stuck detection or action events should be emitted.
	stuckEvents, _ := es.List(state.EventFilter{Type: state.EventControllerStuckDetected})
	if len(stuckEvents) != 0 {
		t.Errorf("expected no stuck detection for healthy story, got %d", len(stuckEvents))
	}
	actionEvents, _ := es.List(state.EventFilter{Type: state.EventControllerAction})
	if len(actionEvents) != 0 {
		t.Errorf("expected no action for healthy story, got %d", len(actionEvents))
	}
}

func TestController_RunLoop_DisabledIsNoop(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{Enabled: false}
	ctrl := NewController(cfg, nil, es, ps)

	// RunLoop should return immediately when disabled.
	done := make(chan struct{})
	go func() {
		ctrl.RunLoop(context.Background())
		close(done)
	}()

	select {
	case <-done:
		// OK — returned immediately.
	case <-time.After(500 * time.Millisecond):
		t.Error("RunLoop did not return when disabled")
	}
}

func TestController_RunLoop_CancelledByContext(t *testing.T) {
	es, ps := newControllerTestStores(t)
	cfg := config.ControllerConfig{
		Enabled:   true,
		IntervalS: 1,
	}
	ctrl := NewController(cfg, nil, es, ps)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ctrl.RunLoop(ctx)
		close(done)
	}()

	// Cancel after a short delay.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// OK — shut down cleanly.
	case <-time.After(2 * time.Second):
		t.Error("RunLoop did not exit after context cancellation")
	}
}

func TestController_RegisterCancel(t *testing.T) {
	es, ps := newControllerTestStores(t)
	ctrl := NewController(config.ControllerConfig{}, nil, es, ps)

	ctrl.RegisterCancel("s-001", func() {})
	ctrl.RegisterCancel("s-002", func() {})

	ctrl.mu.Lock()
	count := len(ctrl.cancelFuncs)
	ctrl.mu.Unlock()

	if count != 2 {
		t.Errorf("expected 2 registered cancel funcs, got %d", count)
	}
}
