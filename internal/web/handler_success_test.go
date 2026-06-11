package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TestHandleKill_SuccessPathEmitsTerminatedEvent covers the "happy
// path" through handleKill: a known agent, a session that doesn't
// exist (tmux returns an error and we keep going), and a
// AGENT_TERMINATED event landing in the event store.
//
// Closes the gap that left handleKill at 48% — only the negative
// branches were previously exercised. The tmux command will fail in
// the test (no live session); the handler is documented to ignore
// that failure ("session may already be dead") so success is
// expected.
func TestHandleKill_SuccessPathEmitsTerminatedEvent(t *testing.T) {
	s := newTestServer(t)
	// AGENT_SPAWNED isn't projected into the agents table; insert
	// directly so handleKill's ListAgents call can find the row.
	if err := s.projStore.InsertAgent("agent-test-001", "dev", "claude", "tmux", "nxd-test-session"); err != nil {
		t.Fatalf("InsertAgent: %v", err)
	}

	resp := s.handleKill(mustMarshal(t, agentPayload{AgentID: "agent-test-001"}))

	if !resp.Success {
		t.Fatalf("expected Success=true, got %+v", resp)
	}
	if !strings.Contains(resp.Message, "agent-test-001") {
		t.Errorf("response message should mention agent id: %q", resp.Message)
	}

	terminated, err := s.eventStore.List(state.EventFilter{Type: state.EventAgentTerminated})
	if err != nil {
		t.Fatalf("list terminated: %v", err)
	}
	if len(terminated) == 0 {
		t.Fatal("AGENT_TERMINATED event should have been appended")
	}
	payload := state.DecodePayload(terminated[0].Payload)
	if payload["source"] != "dashboard" {
		t.Errorf("payload source = %v, want dashboard", payload["source"])
	}
}

// TestHandleKill_StoreErrorReturnsErrorResponse covers the projStore
// listing failure path. Closing the projection store before invocation
// triggers the database-closed error on ListAgents.
func TestHandleKill_StoreErrorReturnsErrorResponse(t *testing.T) {
	s := newTestServer(t)
	// Close the projection store; ListAgents now returns "database is closed".
	if err := s.projStore.Close(); err != nil {
		t.Fatalf("close projstore: %v", err)
	}

	resp := s.handleKill(mustMarshal(t, agentPayload{AgentID: "agent-x"}))
	if resp.Success {
		t.Fatal("expected Success=false on store error")
	}
	if !strings.Contains(resp.Message, "store error") {
		t.Errorf("expected 'store error' message, got %q", resp.Message)
	}
}

// TestHandleKill_MalformedPayload exercises the json.Unmarshal failure
// branch (json that doesn't decode into agentPayload). The handler
// should return an "invalid agent_id" message rather than panicking.
func TestHandleKill_MalformedPayload(t *testing.T) {
	s := newTestServer(t)
	resp := s.handleKill(json.RawMessage(`{"agent_id": 12345}`)) // wrong type
	if resp.Success {
		t.Fatal("expected Success=false on malformed payload")
	}
	if !strings.Contains(resp.Message, "invalid agent_id") {
		t.Errorf("expected 'invalid agent_id' message, got %q", resp.Message)
	}
}

// TestHandleKill_RejectsUnsafeStoredSessionName guards the
// defense-in-depth check on the projection-read sessionName: even though
// the spawn-time path validates names before insertion, a tampered or
// migrated projection could carry a value like "nxd-session.0" — tmux
// `.0` syntax then targets pane 0 of that session, killing the wrong
// thing. handleKill now re-validates via sanitize.ValidTmuxTarget and
// refuses the kill on failure.
func TestHandleKill_RejectsUnsafeStoredSessionName(t *testing.T) {
	s := newTestServer(t)
	// Insert an agent whose SessionName contains a tmux pane separator —
	// passes ValidIdentifier (which permits '.') but must fail the
	// stricter ValidTmuxTarget.
	if err := s.projStore.InsertAgent("agent-bad-session", "dev", "claude", "tmux", "nxd-session.0"); err != nil {
		t.Fatalf("InsertAgent: %v", err)
	}

	resp := s.handleKill(mustMarshal(t, agentPayload{AgentID: "agent-bad-session"}))
	if resp.Success {
		t.Fatal("expected Success=false when session name has tmux pane separator")
	}
	if !strings.Contains(resp.Message, "failed validation") {
		t.Errorf("expected 'failed validation' in message, got %q", resp.Message)
	}
}

// TestHandleKill_AgentInListButNoSession covers the path where an
// agent exists in the projection but its SessionName field is empty.
// The handler refuses to issue a tmux kill in that case.
func TestHandleKill_AgentInListButNoSession(t *testing.T) {
	s := newTestServer(t)
	// Insert agent with empty session_name — handler must refuse.
	if err := s.projStore.InsertAgent("agent-no-session", "dev", "claude", "tmux", ""); err != nil {
		t.Fatalf("InsertAgent: %v", err)
	}

	resp := s.handleKill(mustMarshal(t, agentPayload{AgentID: "agent-no-session"}))
	if resp.Success {
		t.Fatal("expected Success=false when session_name empty")
	}
	if !strings.Contains(resp.Message, "no session") {
		t.Errorf("expected 'no session' message, got %q", resp.Message)
	}
}
