package state

import (
	"path/filepath"
	"testing"
)

// agentStatus returns the projected status of a single agent by id.
func agentStatus(t *testing.T, ps *SQLiteStore, agentID string) string {
	t.Helper()
	agents, err := ps.ListAgents(AgentFilter{})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	for _, a := range agents {
		if a.ID == agentID {
			return a.Status
		}
	}
	t.Fatalf("agent %q not found in projection", agentID)
	return ""
}

// TestProjectAgentStatus_LifecycleTransitions verifies that the agent
// lifecycle events actually mutate agents.status. Before the fix these events
// had no case in Project() and were silently dropped, so the column stayed
// frozen at 'idle' and `nxd agents --status {stuck,terminated,active}` could
// never return a row.
func TestProjectAgentStatus_LifecycleTransitions(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()

	// Spawn two agents on two stories (session names distinct).
	spawn := func(agentID, storyID, session string) {
		evt := NewEvent(EventAgentSpawned, agentID, storyID, map[string]any{
			"role":         "junior",
			"session_name": session,
		})
		if err := ps.Project(evt); err != nil {
			t.Fatalf("project AGENT_SPAWNED %s: %v", agentID, err)
		}
	}
	spawn("agent-dash", "S1", "nxd-S1")
	spawn("agent-ctrl", "S2", "nxd-S2")

	if got := agentStatus(t, ps, "agent-dash"); got != "idle" {
		t.Fatalf("freshly spawned agent status = %q, want idle", got)
	}

	// Dashboard kill: keyed by AgentID, StoryID empty.
	kill := NewEvent(EventAgentTerminated, "agent-dash", "", map[string]any{
		"reason": "killed from dashboard",
	})
	if err := ps.Project(kill); err != nil {
		t.Fatalf("project AGENT_TERMINATED (by agent id): %v", err)
	}
	if got := agentStatus(t, ps, "agent-dash"); got != "terminated" {
		t.Errorf("after dashboard kill, agent-dash status = %q, want terminated", got)
	}

	// Controller cancel: AgentID is the actor "controller", the real target is
	// StoryID → must resolve via current_story_id.
	cancel := NewEvent(EventAgentTerminated, "controller", "S2", map[string]any{
		"reason": "controller cancelled stuck agent",
	})
	if err := ps.Project(cancel); err != nil {
		t.Fatalf("project AGENT_TERMINATED (by story): %v", err)
	}
	if got := agentStatus(t, ps, "agent-ctrl"); got != "terminated" {
		t.Errorf("after controller cancel, agent-ctrl status = %q, want terminated", got)
	}
	// The controller-actor id must NOT have been inserted/updated as an agent.
	for _, a := range mustListAgents(t, ps) {
		if a.ID == "controller" {
			t.Error("controller actor id must not appear as an agent row")
		}
	}

	// The --status filter now actually finds terminated agents.
	term, err := ps.ListAgents(AgentFilter{Status: "terminated"})
	if err != nil {
		t.Fatalf("ListAgents(terminated): %v", err)
	}
	if len(term) != 2 {
		t.Errorf("want 2 terminated agents, got %d", len(term))
	}
}

// TestProjectAgentStatus_StuckBySession covers the watchdog's AGENT_STUCK,
// which carries neither AgentID nor StoryID — only the tmux session name in its
// payload.
func TestProjectAgentStatus_StuckBySession(t *testing.T) {
	ps, err := NewSQLiteStore(filepath.Join(t.TempDir(), "nxd.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer func() { _ = ps.Close() }()

	spawn := NewEvent(EventAgentSpawned, "agent-x", "S9", map[string]any{
		"role":         "junior",
		"session_name": "nxd-S9",
	})
	if err := ps.Project(spawn); err != nil {
		t.Fatalf("project AGENT_SPAWNED: %v", err)
	}

	stuck := NewEvent(EventAgentStuck, "", "", map[string]any{
		"session_name": "nxd-S9",
		"stuck_for_s":  300,
	})
	if err := ps.Project(stuck); err != nil {
		t.Fatalf("project AGENT_STUCK: %v", err)
	}
	if got := agentStatus(t, ps, "agent-x"); got != "stuck" {
		t.Errorf("after AGENT_STUCK, agent-x status = %q, want stuck", got)
	}

	// AGENT_RESUMED brings it back to active (keyed by agent id).
	resumed := NewEvent(EventAgentResumed, "agent-x", "", nil)
	if err := ps.Project(resumed); err != nil {
		t.Fatalf("project AGENT_RESUMED: %v", err)
	}
	if got := agentStatus(t, ps, "agent-x"); got != "active" {
		t.Errorf("after AGENT_RESUMED, agent-x status = %q, want active", got)
	}

	// An AGENT_STUCK with no identifier at all is a harmless no-op, not an error.
	orphan := NewEvent(EventAgentStuck, "", "", map[string]any{"stuck_for_s": 1})
	if err := ps.Project(orphan); err != nil {
		t.Errorf("AGENT_STUCK with no identifier should be a no-op, got %v", err)
	}
}

func mustListAgents(t *testing.T, ps *SQLiteStore) []Agent {
	t.Helper()
	agents, err := ps.ListAgents(AgentFilter{})
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	return agents
}
