// internal/web/server_test.go
package web

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newTestServer creates a Server backed by real (temp-dir) stores for testing.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	tmpDir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(tmpDir, "events.jsonl"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ps, err := state.NewSQLiteStore(filepath.Join(tmpDir, "proj.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() {
		es.Close()  //nolint:errcheck
		ps.Close()  //nolint:errcheck
	})
	return NewServer(es, ps, 0, state.ReqFilter{}, tmpDir, nil)
}

// seedRequirement emits and projects a REQ_SUBMITTED event and returns the requirement ID.
func seedRequirement(t *testing.T, s *Server) string {
	t.Helper()
	id := "req-test-001"
	evt := state.NewEvent(state.EventReqSubmitted, "system", "", map[string]any{
		"id":          id,
		"title":       "Test Requirement",
		"description": "A test requirement for handler tests",
		"repo_path":   "/tmp/test-repo",
	})
	if err := s.eventStore.Append(evt); err != nil {
		t.Fatalf("seed requirement append: %v", err)
	}
	if err := s.projStore.Project(evt); err != nil {
		t.Fatalf("seed requirement project: %v", err)
	}
	return id
}

// seedStory emits and projects a STORY_CREATED event under the given requirement
// and returns the story ID.
func seedStory(t *testing.T, s *Server, reqID string) string {
	t.Helper()
	id := "story-test-001"
	evt := state.NewEvent(state.EventStoryCreated, "system", id, map[string]any{
		"id":                  id,
		"req_id":              reqID,
		"title":               "Test Story",
		"description":         "A test story",
		"acceptance_criteria": "It works",
		"complexity":          3,
	})
	if err := s.eventStore.Append(evt); err != nil {
		t.Fatalf("seed story append: %v", err)
	}
	if err := s.projStore.Project(evt); err != nil {
		t.Fatalf("seed story project: %v", err)
	}
	return id
}

// seedAgent emits and projects an AGENT_SPAWNED event and returns the agent ID.
func seedAgent(t *testing.T, s *Server, sessionName string) string {
	t.Helper()
	id := "agent-test-001"
	evt := state.NewEvent(state.EventAgentSpawned, id, "", map[string]any{
		"id":           id,
		"type":         "dev",
		"model":        "claude",
		"runtime":      "tmux",
		"session_name": sessionName,
	})
	if err := s.eventStore.Append(evt); err != nil {
		t.Fatalf("seed agent append: %v", err)
	}
	if err := s.projStore.Project(evt); err != nil {
		t.Fatalf("seed agent project: %v", err)
	}
	return id
}

// mustMarshal marshals v to JSON or fails the test.
func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustMarshal: %v", err)
	}
	return b
}

// --- tests ---

func TestHandleCommand_UnknownAction(t *testing.T) {
	s := newTestServer(t)
	resp := s.HandleCommand("nonexistent_action", json.RawMessage(`{}`))
	if resp.Success {
		t.Error("expected Success=false for unknown action")
	}
	if resp.Action != "nonexistent_action" {
		t.Errorf("expected Action=nonexistent_action, got %q", resp.Action)
	}
}

func TestHandlePause_Success(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	resp := s.HandleCommand("pause_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))
	if !resp.Success {
		t.Errorf("expected Success=true, got message: %s", resp.Message)
	}

	// Verify projection was updated.
	req, err := s.projStore.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("GetRequirement: %v", err)
	}
	if req.Status != "paused" {
		t.Errorf("expected status=paused, got %q", req.Status)
	}
}

func TestHandlePause_AlreadyPaused(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	// Pause once.
	s.HandleCommand("pause_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))
	// Pause again — should report already paused, still succeed.
	resp := s.HandleCommand("pause_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))
	if !resp.Success {
		t.Errorf("expected Success=true for idempotent pause, got: %s", resp.Message)
	}
}

func TestHandlePause_NotFound(t *testing.T) {
	s := newTestServer(t)

	resp := s.HandleCommand("pause_requirement", mustMarshal(t, map[string]any{"req_id": "nonexistent"}))
	if resp.Success {
		t.Error("expected Success=false for unknown requirement")
	}
}

func TestHandlePause_InvalidPayload(t *testing.T) {
	s := newTestServer(t)

	resp := s.HandleCommand("pause_requirement", json.RawMessage(`{"req_id":""}`))
	if resp.Success {
		t.Error("expected Success=false for empty req_id")
	}
}

func TestHandleResume_Success(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	// Pause first.
	s.HandleCommand("pause_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))

	resp := s.HandleCommand("resume_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))
	if !resp.Success {
		t.Errorf("expected Success=true, got: %s", resp.Message)
	}

	req, err := s.projStore.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("GetRequirement: %v", err)
	}
	// EventReqResumed projects to "planned" status.
	if req.Status != "planned" {
		t.Errorf("expected status=planned after resume, got %q", req.Status)
	}
}

func TestHandleResume_NotPaused(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	resp := s.HandleCommand("resume_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))
	if resp.Success {
		t.Error("expected Success when requirement is not paused")
	}
}

func TestHandleRetry_Success(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	resp := s.HandleCommand("retry_story", mustMarshal(t, map[string]any{"story_id": storyID}))
	if !resp.Success {
		t.Errorf("expected Success=true, got: %s", resp.Message)
	}

	// Story should be reset to draft (from EventStoryReviewFailed) and tier 0.
	story, err := s.projStore.GetStory(storyID)
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	if story.Status != "draft" {
		t.Errorf("expected status=draft after retry, got %q", story.Status)
	}
	if story.EscalationTier != 0 {
		t.Errorf("expected escalation_tier=0 after retry, got %d", story.EscalationTier)
	}
}

func TestHandleRetry_NotFound(t *testing.T) {
	s := newTestServer(t)

	resp := s.HandleCommand("retry_story", mustMarshal(t, map[string]any{"story_id": "nonexistent"}))
	if resp.Success {
		t.Error("expected Success=false for unknown story")
	}
}

func TestHandleReassign_Success(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	resp := s.HandleCommand("reassign_story", mustMarshal(t, map[string]any{
		"story_id":    storyID,
		"target_tier": 2,
	}))
	if !resp.Success {
		t.Errorf("expected Success=true, got: %s", resp.Message)
	}

	story, err := s.projStore.GetStory(storyID)
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	// After reassign: escalation event sets tier=2, then review_failed resets to draft.
	if story.EscalationTier != 2 {
		t.Errorf("expected escalation_tier=2, got %d", story.EscalationTier)
	}
	if story.Status != "draft" {
		t.Errorf("expected status=draft after reassign, got %q", story.Status)
	}
}

func TestHandleReassign_InvalidTier(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	resp := s.HandleCommand("reassign_story", mustMarshal(t, map[string]any{
		"story_id":    storyID,
		"target_tier": 99,
	}))
	if resp.Success {
		t.Error("expected Success=false for out-of-range target_tier")
	}
}

func TestHandleEscalate_Success(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	resp := s.HandleCommand("escalate_story", mustMarshal(t, map[string]any{"story_id": storyID}))
	if !resp.Success {
		t.Errorf("expected Success=true, got: %s", resp.Message)
	}

	story, err := s.projStore.GetStory(storyID)
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	// Story starts at tier 0, escalate moves it to tier 1.
	if story.EscalationTier != 1 {
		t.Errorf("expected escalation_tier=1 after escalate, got %d", story.EscalationTier)
	}
}

func TestHandleEscalate_CapAtMax(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	// Escalate four times — should cap at 3.
	for i := 0; i < 4; i++ {
		s.HandleCommand("escalate_story", mustMarshal(t, map[string]any{"story_id": storyID}))
	}

	story, err := s.projStore.GetStory(storyID)
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	if story.EscalationTier > maxEscalationTier {
		t.Errorf("escalation_tier %d exceeds max %d", story.EscalationTier, maxEscalationTier)
	}
}

func TestHandleKill_InvalidIDFormat(t *testing.T) {
	s := newTestServer(t)

	resp := s.HandleCommand("kill_agent", mustMarshal(t, map[string]any{"agent_id": "bad id with spaces"}))
	if resp.Success {
		t.Error("expected Success=false for invalid agent_id format")
	}
}

func TestHandleKill_AgentNotFound(t *testing.T) {
	s := newTestServer(t)

	resp := s.HandleCommand("kill_agent", mustMarshal(t, map[string]any{"agent_id": "nonexistent-agent"}))
	if resp.Success {
		t.Error("expected Success=false for unknown agent")
	}
}

func TestHandleKill_EmptyAgentID(t *testing.T) {
	s := newTestServer(t)

	resp := s.HandleCommand("kill_agent", mustMarshal(t, map[string]any{"agent_id": ""}))
	if resp.Success {
		t.Error("expected Success=false for empty agent_id")
	}
}

func TestHandleEdit_Success(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	resp := s.HandleCommand("edit_story", mustMarshal(t, map[string]any{
		"story_id":    storyID,
		"title":       "Updated Title",
		"description": "Updated description",
		"complexity":  5,
	}))
	if !resp.Success {
		t.Errorf("expected Success=true, got: %s", resp.Message)
	}

	story, err := s.projStore.GetStory(storyID)
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	if story.Title != "Updated Title" {
		t.Errorf("expected title=Updated Title, got %q", story.Title)
	}
	if story.Complexity != 5 {
		t.Errorf("expected complexity=5, got %d", story.Complexity)
	}
	// projectStoryRewritten resets status to draft.
	if story.Status != "draft" {
		t.Errorf("expected status=draft after edit, got %q", story.Status)
	}
}

func TestHandleEdit_NoChanges(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	resp := s.HandleCommand("edit_story", mustMarshal(t, map[string]any{"story_id": storyID}))
	if resp.Success {
		t.Error("expected Success=false when no changes provided")
	}
}

func TestHandleEdit_StoryNotFound(t *testing.T) {
	s := newTestServer(t)

	resp := s.HandleCommand("edit_story", mustMarshal(t, map[string]any{
		"story_id": "nonexistent",
		"title":    "New Title",
	}))
	if resp.Success {
		t.Error("expected Success=false for unknown story")
	}
}

func TestHandleEdit_AcceptanceCriteria(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	resp := s.HandleCommand("edit_story", mustMarshal(t, map[string]any{
		"story_id":            storyID,
		"acceptance_criteria": "Must pass all checks",
	}))
	if !resp.Success {
		t.Errorf("expected Success=true, got: %s", resp.Message)
	}

	story, err := s.projStore.GetStory(storyID)
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	if story.AcceptanceCriteria != "Must pass all checks" {
		t.Errorf("expected acceptance_criteria updated, got %q", story.AcceptanceCriteria)
	}
}

func TestHandleApproveRequirement_Success(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	// Move to pending_review status first.
	pendingEvt := state.NewEvent(state.EventReqPendingReview, "system", "", map[string]any{"id": reqID})
	s.eventStore.Append(pendingEvt)
	s.projStore.Project(pendingEvt)

	resp := s.HandleCommand("approve_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))
	if !resp.Success {
		t.Errorf("expected Success=true, got: %s", resp.Message)
	}

	req, err := s.projStore.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("GetRequirement: %v", err)
	}
	if req.Status != "planned" {
		t.Errorf("expected status=planned after approve, got %q", req.Status)
	}
}

func TestHandleApproveRequirement_NotPendingReview(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	// Requirement is in 'pending' status, not 'pending_review'.
	resp := s.HandleCommand("approve_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))
	if resp.Success {
		t.Error("expected Success=false when requirement is not pending_review")
	}
}

func TestHandleApproveRequirement_NotFound(t *testing.T) {
	s := newTestServer(t)

	resp := s.HandleCommand("approve_requirement", mustMarshal(t, map[string]any{"req_id": "nonexistent"}))
	if resp.Success {
		t.Error("expected Success=false for unknown requirement")
	}
}

func TestHandleRejectRequirement_Success(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	// Move to pending_review status.
	pendingEvt := state.NewEvent(state.EventReqPendingReview, "system", "", map[string]any{"id": reqID})
	s.eventStore.Append(pendingEvt)
	s.projStore.Project(pendingEvt)

	resp := s.HandleCommand("reject_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))
	if !resp.Success {
		t.Errorf("expected Success=true, got: %s", resp.Message)
	}

	req, err := s.projStore.GetRequirement(reqID)
	if err != nil {
		t.Fatalf("GetRequirement: %v", err)
	}
	if req.Status != "rejected" {
		t.Errorf("expected status=rejected after reject, got %q", req.Status)
	}
}

func TestHandleRejectRequirement_NotPendingReview(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	resp := s.HandleCommand("reject_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))
	if resp.Success {
		t.Error("expected Success=false when requirement is not pending_review")
	}
}

func TestHandleMergeStory_Success(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	// Move story to merge_ready.
	mergeReadyEvt := state.NewEvent(state.EventStoryMergeReady, "system", storyID, nil)
	s.eventStore.Append(mergeReadyEvt)
	s.projStore.Project(mergeReadyEvt)

	resp := s.HandleCommand("merge_story", mustMarshal(t, map[string]any{"story_id": storyID}))
	if !resp.Success {
		t.Errorf("expected Success=true, got: %s", resp.Message)
	}

	story, err := s.projStore.GetStory(storyID)
	if err != nil {
		t.Fatalf("GetStory: %v", err)
	}
	if story.Status != "merged" {
		t.Errorf("expected status=merged after merge, got %q", story.Status)
	}
}

func TestHandleMergeStory_NotMergeReady(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	// Story is in 'draft' status, not 'merge_ready'.
	resp := s.HandleCommand("merge_story", mustMarshal(t, map[string]any{"story_id": storyID}))
	if resp.Success {
		t.Error("expected Success=false when story is not merge_ready")
	}
}

func TestHandleMergeStory_NotFound(t *testing.T) {
	s := newTestServer(t)

	resp := s.HandleCommand("merge_story", mustMarshal(t, map[string]any{"story_id": "nonexistent"}))
	if resp.Success {
		t.Error("expected Success=false for unknown story")
	}
}

func TestHandleCommand_EventsEmitted(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)

	countBefore, err := s.eventStore.Count(state.EventFilter{})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}

	s.HandleCommand("pause_requirement", mustMarshal(t, map[string]any{"req_id": reqID}))

	countAfter, err := s.eventStore.Count(state.EventFilter{})
	if err != nil {
		t.Fatalf("Count after: %v", err)
	}
	if countAfter <= countBefore {
		t.Error("expected at least one new event to be emitted after pause_requirement")
	}
}
