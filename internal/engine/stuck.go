package engine

import (
	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Stuck detection for CLI (tmux) agents.
//
// Native agents emit STORY_PROGRESS on every iteration, so the controller can
// see them working. A tmux agent emits nothing between STORY_STARTED and
// STORY_COMPLETED; the only progress signal is its pane output, which the
// watchdog fingerprints on every poll. observeAgent bridges the two: an
// output change becomes one AGENT_CHECKPOINT (at most one per poll per
// agent) so Controller.lastProgressTime sees progress, and the watchdog's
// AGENT_STUCK (once per frozen-output episode) is stamped with the agent,
// story and attempt so it is attributable.

// agentIdentity extracts the watchdog identity from an active agent.
func agentIdentity(ag ActiveAgent) AgentIdentity {
	return AgentIdentity{
		AgentID:     ag.Assignment.AgentID,
		StoryID:     ag.Assignment.StoryID,
		AttemptID:   ag.Assignment.AttemptID,
		RuntimeName: ag.RuntimeName,
	}
}

// observeAgent runs the watchdog for one CLI agent and turns an observed
// output change into a lightweight AGENT_CHECKPOINT progress event.
func (m *Monitor) observeAgent(sessionName string, rt runtime.Runtime, ag ActiveAgent) CheckResult {
	result := m.watchdog.CheckAgent(sessionName, rt, agentIdentity(ag))
	if result.OutputChanged {
		emitEventOrLog(m.eventStore, m.projStore,
			state.NewEventForAttempt(state.EventAgentCheckpoint, ag.Assignment.AgentID, ag.Assignment.StoryID, ag.Assignment.AttemptID, map[string]any{
				"source":       "watchdog",
				"session_name": sessionName,
				"message":      "pane output changed",
			}))
	}
	if result.PromptEpisodeStarted {
		m.pauseForPermissionPrompt(sessionName, ag)
	}
	return result
}
