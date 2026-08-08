package state

import (
	"path/filepath"
	"testing"
)

// TestProject_AllEventTypes_NoErrors drives one event of every
// supported EventType through SQLiteStore.Project. Each case in the
// switch statement either updates the projection or returns nil for
// unhandled types — both are valid. The point is to ensure no case
// panics, returns spurious errors, or leaves the DB in an
// inconsistent state.
//
// Without this batch, Project's per-function coverage stayed at
// ~46% because tests only seeded a handful of event types directly.
func TestProject_AllEventTypes_NoErrors(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()

	// Order matters: requirements must be projected before their
	// stories. seedReq/seedStory both project through Project()
	// itself so we exercise those cases too.
	feed := []struct {
		typ     EventType
		storyID string
		payload map[string]any
	}{
		{EventReqSubmitted, "", map[string]any{"id": "R", "title": "t", "description": "d", "repo_path": "/tmp"}},
		{EventReqAnalyzed, "", map[string]any{"id": "R"}},
		{EventReqPlanned, "", map[string]any{"id": "R"}},
		{EventReqPaused, "", map[string]any{"id": "R"}},
		{EventReqResumed, "", map[string]any{"id": "R"}},
		{EventReqClassified, "", map[string]any{"id": "R", "type": "feature", "confidence": 0.9}},
		{EventInvestigationCompleted, "", map[string]any{"id": "R"}},
		{EventReqPendingReview, "", map[string]any{"id": "R"}},
		{EventStoryCreated, "S1", map[string]any{"id": "S1", "req_id": "R", "title": "story", "description": "d", "complexity": 3}},
		{EventStoryEstimated, "S1", map[string]any{"id": "S1"}},
		{EventStoryAssigned, "S1", map[string]any{"id": "S1", "role": "junior", "branch": "story/S1", "agent_id": "agent-1"}},
		{EventStoryStarted, "S1", map[string]any{"id": "S1"}},
		{EventStoryProgress, "S1", map[string]any{"iteration": 1, "phase": "read", "detail": "scanning"}},
		{EventStoryReviewRequested, "S1", nil},
		{EventStoryReviewPassed, "S1", nil},
		{EventStoryReviewFailed, "S1", map[string]any{"reason": "rejected"}},
		{EventStoryQAStarted, "S1", nil},
		{EventStoryQAPassed, "S1", nil},
		{EventStoryQAFailed, "S1", map[string]any{"reason": "qa fail"}},
		{EventStoryPRCreated, "S1", map[string]any{"pr_number": 42, "pr_url": "https://example/42"}},
		{EventStoryMergeReady, "S1", nil},
		{EventStoryMerged, "S1", nil},
		{EventStoryRecovery, "S1", map[string]any{"type": "worktree_pruned", "description": "wt removed"}},
		{EventStoryEscalated, "S1", map[string]any{"from_tier": 0, "to_tier": 1, "reason": "stuck"}},
		{EventStoryRewritten, "S1", map[string]any{"changes": map[string]any{"title": "Updated"}}},
		{EventStoryReset, "S1", map[string]any{"reason": "ops reset"}},
		{EventStoryCompleted, "S1", map[string]any{"iterations": 1}},
		{EventReqCompleted, "", map[string]any{"id": "R"}},
		// Story under a 2nd req for the rejected/split paths.
		{EventReqSubmitted, "", map[string]any{"id": "R2", "title": "t2", "description": "d", "repo_path": "/tmp"}},
		{EventReqRejected, "", map[string]any{"id": "R2", "reason": "operator declined"}},
		{EventStoryCreated, "S2", map[string]any{"id": "S2", "req_id": "R", "title": "split-parent", "complexity": 5}},
		{EventStorySplit, "S2", map[string]any{"child_story_ids": []string{"S2-a"}}},
		// Unknown event type → default branch returns nil.
		{EventType("UNKNOWN_TEST_TYPE"), "", nil},
	}

	for _, step := range feed {
		t.Run(string(step.typ), func(t *testing.T) {
			evt := NewEvent(step.typ, "test", step.storyID, step.payload)
			if err := ps.Project(evt); err != nil {
				t.Errorf("Project(%s): %v", step.typ, err)
			}
		})
	}

	// After all events, the projection should know about the
	// requirements and at least one story.
	reqs, err := ps.ListRequirementsFiltered(ReqFilter{})
	if err != nil {
		t.Fatalf("ListRequirementsFiltered: %v", err)
	}
	if len(reqs) < 1 {
		t.Errorf("expected at least 1 requirement in projection; got %d", len(reqs))
	}
}

// TestProject_AgentTerminated_TransitionsRow locks in the wiring fix: an
// AGENT_TERMINATED event (dashboard kill / controller auto-cancel) must
// transition the agent row to status='terminated' and clear its
// current_story_id. Before the fix the event fell through Project's default
// case and silently no-op'd, so a killed agent stayed status='idle' with a
// live session→story mapping that crash recovery still consumed.
func TestProject_AgentTerminated_TransitionsRow(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()

	spawn := NewEvent(EventAgentSpawned, "agent-42", "S1", map[string]any{
		"role": "junior", "session_name": "nxd-S1",
	})
	if err := ps.Project(spawn); err != nil {
		t.Fatalf("project spawn: %v", err)
	}

	// Precondition: the spawned agent is idle and mapped to its story.
	idle, err := ps.ListAgents(AgentFilter{Status: "idle"})
	if err != nil {
		t.Fatalf("list idle: %v", err)
	}
	if len(idle) != 1 || idle[0].CurrentStoryID != "S1" {
		t.Fatalf("precondition: want 1 idle agent on S1, got %+v", idle)
	}

	// Kill it.
	term := NewEvent(EventAgentTerminated, "agent-42", "", map[string]any{
		"reason": "killed from dashboard", "source": "dashboard",
	})
	if err := ps.Project(term); err != nil {
		t.Fatalf("project terminate: %v", err)
	}

	// The agent must no longer be idle...
	if idle, _ := ps.ListAgents(AgentFilter{Status: "idle"}); len(idle) != 0 {
		t.Errorf("killed agent still listed as idle: %+v", idle)
	}
	// ...it must be terminated with its story mapping cleared.
	term2, err := ps.ListAgents(AgentFilter{Status: "terminated"})
	if err != nil {
		t.Fatalf("list terminated: %v", err)
	}
	if len(term2) != 1 {
		t.Fatalf("want 1 terminated agent, got %d", len(term2))
	}
	if term2[0].CurrentStoryID != "" {
		t.Errorf("terminated agent should have current_story_id cleared, got %q", term2[0].CurrentStoryID)
	}
}
