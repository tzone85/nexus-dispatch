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
	defer ps.Close()

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
