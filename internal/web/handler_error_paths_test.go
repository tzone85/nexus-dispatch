package web

import (
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// closeProjStore is the trigger we use to force the store-error
// branches in the handlers. Closing the projection makes ListAgents,
// GetStory, ListRequirementsFiltered etc. fail with "database is
// closed" — exactly the path the handlers' "store error" return
// is supposed to cover. Without these tests, the error-handling
// branches in handlePause/Resume/Retry/Reassign/Escalate/Edit/
// Approve/Reject/Merge stayed at 0% and silent regressions in any
// of them would only show up in production at the worst time.
func closeProjStore(t *testing.T, s *Server) {
	t.Helper()
	if err := s.projStore.Close(); err != nil {
		t.Fatalf("close projstore: %v", err)
	}
}

// TestHandlePause_StoreError triggers the findRequirement branch's
// error return.
func TestHandlePause_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	resp := s.handlePause(mustMarshal(t, reqPayload{ReqID: "REQ-X"}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
	if !strings.Contains(resp.Message, "store error") {
		t.Errorf("expected 'store error' message, got %q", resp.Message)
	}
}

// TestHandleResume_StoreError covers handleResume's findRequirement
// failure path (projStore closed → can't list requirements).
func TestHandleResume_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	resp := s.handleResume(mustMarshal(t, reqPayload{ReqID: "REQ-X"}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
	if !strings.Contains(resp.Message, "store error") {
		t.Errorf("expected 'store error' message, got %q", resp.Message)
	}
}

// TestHandleRetry_StoreError covers handleRetry's findStory failure
// path. The dashboard's ↻ button must surface the failure rather
// than silently no-op.
func TestHandleRetry_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	resp := s.handleRetry(mustMarshal(t, storyPayload{StoryID: "STORY-X"}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
	if !strings.Contains(resp.Message, "store error") {
		t.Errorf("expected 'store error' message, got %q", resp.Message)
	}
}

// TestHandleReassign_StoreError mirrors retry but for the ↑ tier-jump
// button.
func TestHandleReassign_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	resp := s.handleReassign(mustMarshal(t, storyPayload{StoryID: "STORY-X", TargetTier: 1}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
}

// TestHandleEscalate_StoryNotFound triggers the not-found branch (vs
// the store-error one tested by retry/reassign). findStory returns
// (nil, nil) when the projection is healthy but the story doesn't
// exist — different code path.
func TestHandleEscalate_StoryNotFound(t *testing.T) {
	s := newTestServer(t)

	resp := s.handleEscalate(mustMarshal(t, storyPayload{StoryID: "STORY-NOT-THERE"}))
	if resp.Success {
		t.Fatal("expected Success=false for unknown story")
	}
	if !strings.Contains(resp.Message, "not found") {
		t.Errorf("expected 'not found' in message, got %q", resp.Message)
	}
}

// TestHandleEscalate_StoreError covers the projStore-closed branch
// for the ↑ escalate button.
func TestHandleEscalate_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	resp := s.handleEscalate(mustMarshal(t, storyPayload{StoryID: "STORY-X"}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
	if !strings.Contains(resp.Message, "store error") {
		t.Errorf("expected 'store error' in message, got %q", resp.Message)
	}
}

// TestHandleEdit_NoChangesRejected covers the inline-edit branch
// where every editable field is empty / zero — the handler must
// reject rather than emit an empty STORY_REWRITTEN event.
func TestHandleEdit_NoChangesRejected(t *testing.T) {
	s := newTestServer(t)
	reqID := seedRequirement(t, s)
	storyID := seedStory(t, s, reqID)

	resp := s.handleEdit(mustMarshal(t, editPayload{StoryID: storyID}))
	if resp.Success {
		t.Fatal("expected Success=false when no changes provided")
	}
	if !strings.Contains(resp.Message, "no changes") {
		t.Errorf("expected 'no changes' message, got %q", resp.Message)
	}
}

// TestHandleEdit_StoreError covers the findStory failure path.
func TestHandleEdit_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	resp := s.handleEdit(mustMarshal(t, editPayload{
		StoryID: "STORY-X", Title: "new title",
	}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
}

// TestHandleApproveRequirement_StoreError covers findRequirement
// failure.
func TestHandleApproveRequirement_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	resp := s.handleApproveRequirement(mustMarshal(t, reqPayload{ReqID: "REQ-X"}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
}

// TestHandleRejectRequirement_NotFound mirrors approve.
func TestHandleRejectRequirement_NotFound(t *testing.T) {
	s := newTestServer(t)

	resp := s.handleRejectRequirement(mustMarshal(t, reqPayload{ReqID: "REQ-NOPE"}))
	if resp.Success {
		t.Fatal("expected Success=false for unknown requirement")
	}
}

// TestHandleRejectRequirement_StoreError mirrors approve.
func TestHandleRejectRequirement_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	resp := s.handleRejectRequirement(mustMarshal(t, reqPayload{ReqID: "REQ-X"}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
}

// TestHandleMergeStory_StoreError covers findStory failure.
func TestHandleMergeStory_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	resp := s.handleMergeStory(mustMarshal(t, storyPayload{StoryID: "STORY-X"}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
}

// TestFindRequirement_StoreError covers the helper directly with a
// closed projection — confirms the error propagates rather than
// being swallowed into a (nil, nil) tuple.
func TestFindRequirement_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	_, err := s.findRequirement("anything")
	if err == nil {
		t.Fatal("expected error from findRequirement on closed projstore")
	}
}

// TestFindStory_StoreError mirrors findRequirement for stories.
func TestFindStory_StoreError(t *testing.T) {
	s := newTestServer(t)
	closeProjStore(t, s)

	_, err := s.findStory("anything")
	if err == nil {
		t.Fatal("expected error from findStory on closed projstore")
	}
}

// TestHandlePause_AlreadyPausedIsIdempotent locks the contract that
// pausing an already-paused requirement is a Success=true no-op
// rather than emitting a duplicate REQ_PAUSED event. The dashboard's
// pause button is fire-and-forget, so safe-to-double-press is part
// of the API.
func TestHandlePause_AlreadyPausedIsIdempotent(t *testing.T) {
	s := newTestServer(t)
	id := seedRequirement(t, s)
	pauseEvt := state.NewEvent(state.EventReqPaused, "test", "", map[string]any{"id": id})
	if err := s.eventStore.Append(pauseEvt); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.projStore.Project(pauseEvt); err != nil {
		t.Fatalf("project: %v", err)
	}
	// Count events before second pause.
	before, _ := s.eventStore.List(state.EventFilter{Type: state.EventReqPaused})
	beforeCount := len(before)

	resp := s.handlePause(mustMarshal(t, reqPayload{ReqID: id}))
	if !resp.Success {
		t.Fatalf("expected Success=true (idempotent), got %+v", resp)
	}
	if !strings.Contains(resp.Message, "already paused") {
		t.Errorf("expected 'already paused' message, got %q", resp.Message)
	}
	// No new REQ_PAUSED event must have been emitted.
	after, _ := s.eventStore.List(state.EventFilter{Type: state.EventReqPaused})
	if len(after) != beforeCount {
		t.Errorf("expected no new REQ_PAUSED event; before=%d after=%d", beforeCount, len(after))
	}
}
